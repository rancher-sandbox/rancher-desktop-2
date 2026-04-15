// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the engine reconciler, which mirrors Docker
// engine state into Kubernetes resources.
package controllers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

const (
	appName = "app"

	// containerNamespace is the Docker container namespace name;
	// Docker uses a single namespace called "moby".
	containerNamespace = "moby"

	// controllerName is the SSA field owner for engine-controller applies.
	controllerName = "engine-controller"

	// finalizerFieldOwner is a separate SSA field manager for
	// finalizer-only applies. Keeping it distinct from controllerName
	// prevents a finalizer-only apply from releasing controllerName's
	// ownership of spec — which would prune spec and fail the CRD's
	// required-field validation.
	finalizerFieldOwner = controllerName + "-finalizer"

	// mirrorFinalizer is added to mirror resources so user deletions
	// are forwarded to the container engine before the resource is removed.
	mirrorFinalizer = "engine.rancherdesktop.io/mirror"

	// conditionContainerEngineReady goes to True once the engine
	// controller has connected to Docker and finished the initial sync.
	conditionContainerEngineReady = "ContainerEngineReady"

	// engineMoby is the App.spec.containerEngine.name value that selects
	// the Docker backend. Containerd has no watcher yet and reports
	// NotApplicable.
	engineMoby = "moby"
)

// EngineReconciler watches the App resource for the Running condition and
// manages a Docker watcher goroutine that mirrors engine state into K8s.
type EngineReconciler struct {
	client.Client

	// reconcileChan receives events from the Docker watcher goroutine.
	reconcileChan chan event.GenericEvent

	// apiNamespace mirrors App.spec.namespace (immutable). Reconcile
	// populates it before any mirror operation.
	apiNamespace string

	// watcherMu protects watcher state.
	watcherMu sync.Mutex
	watcher   *dockerWatcher

	// watcherCtx is the parent context for every Docker watcher the
	// reconciler starts. A manager.RunnableFunc cancels it on
	// shutdown, so the watcher outlives individual Reconcile calls
	// but not the manager. Deriving from Reconcile's ctx would leak
	// the Docker client: once the manager context cancels, Reconcile
	// no longer runs, and stopWatcher is unreachable from that path.
	watcherCtx    context.Context
	watcherCancel context.CancelFunc
}

// Reconcile handles App condition changes, Docker watcher lifecycle,
// Container spec.state transitions, and finalizer processing for mirror
// resources.
func (r *EngineReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appv1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	r.apiNamespace = app.GetResourceNamespace()

	running := meta.IsStatusConditionTrue(app.Status.Conditions, "Running")
	engineIsDocker := app.Spec.ContainerEngine.Name == engineMoby

	// Detach a dead watcher under watcherMu, then stop it outside the
	// lock: w.stop() blocks on <-w.done and cli.Close() and would stall
	// concurrent reconciles under MaxConcurrentReconciles > 1.
	var diedWatcher *dockerWatcher
	r.watcherMu.Lock()
	watcherRunning := r.watcher != nil
	if watcherRunning && !r.watcher.alive() {
		diedWatcher = r.watcher
		r.watcher = nil
		watcherRunning = false
	}
	r.watcherMu.Unlock()
	watcherDied := diedWatcher != nil
	if watcherDied {
		diedWatcher.stop()
	}

	// Treat a dead watcher as a transient disconnect and fall through.
	// If wantWatcher is still true below, a fresh watcher's fullSync
	// reconciles drift in place — existing mirror resources keep their
	// identity, so downstream clients see no churn. An actual stop or
	// backend change is handled by the !wantWatcher branch, which
	// sweeps the mirrors.
	if watcherDied {
		log.Info("Docker watcher died, will attempt to reconnect")
	}

	// The watcher runs only when the App is Running on the moby
	// backend. Any other state stops the watcher and sweeps mirror
	// resources. Skip the sweep once ContainerEngineReady already
	// reflects the terminal state, to avoid four empty List calls per
	// unrelated reconcile; a failed sweep leaves the condition pending
	// and the next requeue retries.
	wantWatcher := running && engineIsDocker
	if !wantWatcher {
		if watcherRunning {
			log.Info("Stopping Docker watcher",
				"running", running, "engine", app.Spec.ContainerEngine.Name)
			r.stopWatcher()
		}
		terminalReason := "Stopped"
		terminalStatus := metav1.ConditionFalse
		terminalMessage := "Container engine stopped"
		if running && !engineIsDocker {
			// Report NotApplicable as Status=True so `rdd set
			// running=true containerEngine.name=containerd` stops
			// waiting on ContainerEngineReady. UI consumers that
			// expect Container/Image/Volume mirrors must gate on
			// Reason, not Status alone. The condition will be renamed
			// when containerd mirroring lands.
			terminalReason = "NotApplicable"
			terminalStatus = metav1.ConditionTrue
			terminalMessage = "Engine mirroring is only supported with the moby backend"
		}
		current := meta.FindStatusCondition(app.Status.Conditions, conditionContainerEngineReady)
		alreadyClean := !watcherDied && current != nil && current.Reason == terminalReason && current.Status == terminalStatus
		if !alreadyClean {
			if err := r.cleanupMirrorResources(ctx); err != nil {
				log.Error(err, "Failed to clean up mirror resources")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.setEngineCondition(ctx, &app, terminalStatus, terminalReason, terminalMessage)
	}

	if !watcherRunning {
		log.Info("App is running, starting Docker watcher")
		if err := r.startWatcher(ctx); err != nil {
			log.Error(err, "Failed to start Docker watcher")
			if condErr := r.setEngineCondition(ctx, &app, metav1.ConditionFalse, "ConnectFailed", err.Error()); condErr != nil {
				log.Error(condErr, "Failed to update ContainerEngineReady to ConnectFailed")
			}
			return ctrl.Result{}, err
		}
	}

	// Re-assert Connected on every steady-state reconcile so its
	// observedGeneration tracks the current App generation. When the
	// spec is patched without cycling the watcher (e.g. `rdd set
	// running=true kubernetes.enabled=true`), this is the only place
	// that stamps the new generation into ContainerEngineReady, and
	// rdd set's wait predicate depends on it. setEngineCondition
	// no-ops when nothing changed, so stable reconciles pay nothing.
	if err := r.setEngineCondition(ctx, &app, metav1.ConditionTrue, "Connected", "Container engine synced"); err != nil {
		return ctrl.Result{}, err
	}

	// Both passes issue List calls per reconcile, and every
	// Container/Image/Volume watch event triggers a reconcile — so
	// cost is O(N) per child event. The long-term fix is to split
	// these into per-resource reconcilers with watch predicates
	// (deletionTimestamp set, spec.state changed).
	//
	// Run both passes unconditionally and join their errors. Early
	// return on specsErr would stall every mirror's finalizer behind
	// a single stuck spec.state actuation: the next reconcile would
	// hit the same broken container first and skip finalizers again.
	var specsErr, finErr error
	if err := r.reconcileContainerSpecs(ctx); err != nil {
		specsErr = fmt.Errorf("failed to reconcile container specs: %w", err)
	}
	if err := r.processFinalizers(ctx); err != nil {
		finErr = fmt.Errorf("failed to process finalizers: %w", err)
	}
	return ctrl.Result{}, errors.Join(specsErr, finErr)
}

// startWatcher creates and starts a Docker watcher goroutine. The
// watcher inherits watcherCtx, which cancels only on manager
// shutdown; startWatcher deliberately drops Reconcile's ctx so a
// future ReconciliationTimeout or per-request deadline cannot kill
// the watcher the moment Reconcile returns.
func (r *EngineReconciler) startWatcher(_ context.Context) error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	if r.watcher != nil {
		return nil
	}

	w, err := newDockerWatcher(r.watcherCtx, r.Client, r.apiNamespace, r.reconcileChan)
	if err != nil {
		return err
	}
	r.watcher = w
	return nil
}

// stopWatcher stops the Docker watcher goroutine and waits for it to finish.
func (r *EngineReconciler) stopWatcher() {
	r.watcherMu.Lock()
	w := r.watcher
	r.watcher = nil
	r.watcherMu.Unlock()

	if w != nil {
		w.stop()
	}
}

// setEngineCondition updates the ContainerEngineReady condition on
// the App. The App controller also writes App.Status.Conditions (to
// mirror LimaVM conditions) and controller-runtime does not serialize
// reconciles across controllers, so a naive Update races and 409s.
// retry.RetryOnConflict + re-Get matches removeMirrorResource and
// deleteAllOfType.
func (r *EngineReconciler) setEngineCondition(ctx context.Context, app *appv1alpha1.App, status metav1.ConditionStatus, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appv1alpha1.App{}
		if err := r.Get(ctx, client.ObjectKey{Name: app.Name}, latest); err != nil {
			// Concurrent `rdd svc delete` can remove the App mid-reconcile.
			// NotFound is a no-op, not an error to requeue on.
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		changed := meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionContainerEngineReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: latest.Generation,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	})
}

// cleanupMirrorResources removes every mirror resource the engine
// controller owns. Errors from all four kinds are joined so one stuck
// type does not block the rest.
func (r *EngineReconciler) cleanupMirrorResources(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Cleaning up all mirror resources")

	var errs []error
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ContainerList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete Containers: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.VolumeList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete Volumes: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ImageList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete Images: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ContainerNamespaceList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete ContainerNamespaces: %w", err))
	}
	return errors.Join(errs...)
}

// deleteAllOfType lists and removes every resource of the given kind
// in r.apiNamespace. Engine is authoritative for Container, Image, and
// Volume in the App's namespace: this loop deletes every object, not
// just engine-owned mirrors. Coexistence with another writer to these
// kinds requires an engine-owned label and matching filters here and
// in the sync_*.go full-sync prune paths. Finalizer removal retries
// on conflict; per-item errors are collected so one stuck object does
// not block the rest.
func (r *EngineReconciler) deleteAllOfType(ctx context.Context, list client.ObjectList) error {
	if err := r.List(ctx, list, client.InNamespace(r.apiNamespace)); err != nil {
		return err
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}

	var errs []error
	for _, item := range items {
		obj := item.(client.Object)
		key := client.ObjectKeyFromObject(obj)
		// meta.ExtractList leaves each item's TypeMeta empty (GVK is
		// only on the list), so format the Go type with %T instead of
		// obj.GetObjectKind().
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := obj.DeepCopyObject().(client.Object)
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
				return nil
			}
			return r.Update(ctx, latest)
		})
		if retryErr != nil {
			errs = append(errs, fmt.Errorf("failed to remove finalizer from %T %s: %w",
				obj, obj.GetName(), retryErr))
			continue
		}
		// Delete uses the stale list item, not latest from the retry
		// closure: Delete keys off name+namespace and does not send
		// resourceVersion, so staleness cannot trigger a 409.
		if err := client.IgnoreNotFound(r.Delete(ctx, obj)); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete %T %s: %w",
				obj, obj.GetName(), err))
		}
	}
	return errors.Join(errs...)
}

// reconcileContainerSpecs handles Container spec.state changes by
// calling Docker start/stop. Per-container errors are joined and
// returned so controller-runtime requeues with backoff; without the
// return, a failed start/stop would get exactly one attempt and then
// sit forever.
func (r *EngineReconciler) reconcileContainerSpecs(ctx context.Context) error {
	r.watcherMu.Lock()
	w := r.watcher
	r.watcherMu.Unlock()
	if w == nil {
		return nil
	}

	var containers containersv1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(r.apiNamespace)); err != nil {
		return err
	}

	var errs []error
	for i := range containers.Items {
		c := &containers.Items[i]
		if c.DeletionTimestamp != nil {
			continue
		}
		if err := w.reconcileContainerState(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", c.Name, err))
		}
	}
	return errors.Join(errs...)
}

// processFinalizers handles resources with a deletion timestamp by deleting
// the corresponding Docker object and removing the finalizer.
func (r *EngineReconciler) processFinalizers(ctx context.Context) error {
	r.watcherMu.Lock()
	w := r.watcher
	r.watcherMu.Unlock()
	if w == nil {
		return nil
	}

	// Join errors across all three types so a stuck Container or
	// Image finalizer does not starve pending Volume finalizers.
	return errors.Join(
		r.processContainerFinalizers(ctx, w),
		r.processImageFinalizers(ctx, w),
		r.processVolumeFinalizers(ctx, w),
	)
}

// processContainerFinalizers deletes the Docker-side container for
// every Container pending deletion. The mirror finalizer is only
// stripped when the Docker delete succeeds, so a stuck container keeps
// retrying on later reconciles.
func (r *EngineReconciler) processContainerFinalizers(ctx context.Context, w *dockerWatcher) error {
	var containers containersv1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(r.apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range containers.Items {
		c := &containers.Items[i]
		if c.DeletionTimestamp == nil || !controllerutil.ContainsFinalizer(c, mirrorFinalizer) {
			continue
		}
		if err := w.deleteContainer(ctx, c.Name); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete container %s from Docker: %w", c.Name, err))
			continue
		}
		// Retry on conflict so a stale cache does not force a
		// whole-reconcile requeue just to repeat the idempotent
		// deleteContainer call. NotFound is benign: a concurrent
		// Docker destroy event may already have cleaned up.
		key := client.ObjectKeyFromObject(c)
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &containersv1alpha1.Container{}
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
				return nil
			}
			return r.Update(ctx, latest)
		})
		if retryErr != nil {
			errs = append(errs, fmt.Errorf("failed to remove finalizer from Container %s: %w", c.Name, retryErr))
		}
	}
	return errors.Join(errs...)
}

func (r *EngineReconciler) processImageFinalizers(ctx context.Context, w *dockerWatcher) error {
	var images containersv1alpha1.ImageList
	if err := r.List(ctx, &images, client.InNamespace(r.apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range images.Items {
		img := &images.Items[i]
		if img.DeletionTimestamp == nil || !controllerutil.ContainsFinalizer(img, mirrorFinalizer) {
			continue
		}
		// An Image mirror with no engine-populated status has no
		// Docker reference to forward the delete to (bare-skeleton
		// user create, or the startup race before applyImage ran).
		// Strip the finalizer and let the Delete proceed — symmetric
		// with processVolumeFinalizers' empty-Status.Name guard.
		if img.Status.ID != "" || img.Status.RepoTag != "" {
			if err := w.deleteImage(ctx, img); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete image %s from Docker: %w", img.Name, err))
				continue
			}
		}
		key := client.ObjectKeyFromObject(img)
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &containersv1alpha1.Image{}
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
				return nil
			}
			return r.Update(ctx, latest)
		})
		if retryErr != nil {
			errs = append(errs, fmt.Errorf("failed to remove finalizer from Image %s: %w", img.Name, retryErr))
		}
	}
	return errors.Join(errs...)
}

func (r *EngineReconciler) processVolumeFinalizers(ctx context.Context, w *dockerWatcher) error {
	var volumes containersv1alpha1.VolumeList
	if err := r.List(ctx, &volumes, client.InNamespace(r.apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range volumes.Items {
		v := &volumes.Items[i]
		if v.DeletionTimestamp == nil || !controllerutil.ContainsFinalizer(v, mirrorFinalizer) {
			continue
		}
		// A Volume mirror with empty Status.Name has no Docker-side
		// name to forward a delete to (bare-skeleton user create, or
		// the startup race before applyVolume ran). Strip the
		// finalizer and let the Delete proceed.
		if v.Status.Name != "" {
			if err := w.deleteVolume(ctx, v.Status.Name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete volume %s from Docker: %w", v.Name, err))
				continue
			}
		}
		key := client.ObjectKeyFromObject(v)
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &containersv1alpha1.Volume{}
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
				return nil
			}
			return r.Update(ctx, latest)
		})
		if retryErr != nil {
			errs = append(errs, fmt.Errorf("failed to remove finalizer from Volume %s: %w", v.Name, retryErr))
		}
	}
	return errors.Join(errs...)
}

// SetupWithManager sets up the controller with the Manager.
func (r *EngineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconcileChan = make(chan event.GenericEvent, 1)

	// Create the watcher-scoped context and register a shutdown
	// hook that cancels it and stops the active watcher. The hook
	// fires when the manager context ends. Without it the Docker
	// client stays open after shutdown: stopWatcher is only
	// reachable from Reconcile, and Reconcile stops running once
	// the manager shuts down.
	r.watcherCtx, r.watcherCancel = context.WithCancel(
		logf.IntoContext(context.Background(), mgr.GetLogger().WithName("engine-watcher")),
	)
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		<-ctx.Done()
		r.watcherCancel()
		r.stopWatcher()
		return nil
	})); err != nil {
		return fmt.Errorf("failed to register watcher shutdown hook: %w", err)
	}

	// Map any Container/Image/Volume event to a reconcile of the App singleton.
	enqueueApp := handler.EnqueueRequestsFromMapFunc(
		func(_ context.Context, _ client.Object) []reconcile.Request {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: appName}}}
		},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1alpha1.App{}).
		Named("engine-reconciler").
		WatchesRawSource(source.Channel(r.reconcileChan, enqueueApp)).
		Watches(&containersv1alpha1.Container{}, enqueueApp).
		Watches(&containersv1alpha1.Image{}, enqueueApp).
		Watches(&containersv1alpha1.Volume{}, enqueueApp).
		Complete(r)
}
