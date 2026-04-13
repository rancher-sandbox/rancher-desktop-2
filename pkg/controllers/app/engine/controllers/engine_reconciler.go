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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

const (
	appName = "app"

	// containerNamespace is the Docker container namespace name.
	// Docker always uses a single namespace called "moby".
	containerNamespace = "moby"

	// apiNamespace is the Kubernetes namespace where mirror resources are created.
	apiNamespace = "rancher-desktop"

	// controllerName is used as the field owner for server-side apply.
	controllerName = "engine-controller"

	// finalizerFieldOwner is a separate SSA field manager used only to
	// re-assert the mirror finalizer on existing resources. Keeping it
	// distinct from controllerName prevents a finalizer-only apply from
	// dropping controllerName's ownership of spec — which would cause
	// spec to be pruned and the CRD's required-field validation to fail.
	finalizerFieldOwner = controllerName + "-finalizer"

	// mirrorFinalizer is added to mirror resources so user deletions can
	// be forwarded to Docker before the resource is removed.
	mirrorFinalizer = "engine.rancherdesktop.io/docker-mirror"

	// conditionContainerEngineReady is set on the App resource when the engine
	// controller has connected to Docker and completed the initial sync.
	conditionContainerEngineReady = "ContainerEngineReady"

	// engineMoby is the App spec value that selects the Docker-compatible
	// backend. The engine reconciler only mirrors state for this backend;
	// containerd has no watcher yet and is reported as NotApplicable.
	engineMoby = "moby"
)

// EngineReconciler watches the App resource for the Running condition and
// manages a Docker watcher goroutine that mirrors engine state into K8s.
type EngineReconciler struct {
	client.Client

	// reconcileChan receives events from the Docker watcher goroutine.
	reconcileChan chan event.GenericEvent

	// watcherMu protects watcher state.
	watcherMu sync.Mutex
	watcher   *dockerWatcher
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

	running := meta.IsStatusConditionTrue(app.Status.Conditions, "Running")
	engineIsDocker := app.Spec.ContainerEngine.Name == engineMoby

	r.watcherMu.Lock()
	watcherRunning := r.watcher != nil
	watcherDied := watcherRunning && !r.watcher.alive()
	if watcherDied {
		r.watcher.stop()
		r.watcher = nil
		watcherRunning = false
	}
	r.watcherMu.Unlock()

	// A watcher that dies while the App is still running is treated as a
	// transient disconnect: log it, clear the watcher pointer, and fall
	// through to the normal reconcile flow. If wantWatcher is still true
	// below, the reconciler starts a fresh watcher whose fullSync
	// reconciles any drift in place — downstream clients see no churn
	// because the existing mirror resources keep their identity. An
	// actual stop or backend change is handled by the !wantWatcher
	// branch below, which does sweep the mirrors.
	if watcherDied {
		log.Info("Docker watcher died, will attempt to reconnect")
	}

	// The watcher should only run when the App is Running with the moby
	// backend. In every other state (stopped, containerd, both) we stop
	// the watcher and sweep mirror resources. The sweep is gated on the
	// current ContainerEngineReady reason: once the condition reflects
	// the terminal state, cleanup would be a no-op against an empty
	// namespace, so we short-circuit to avoid four empty List calls per
	// unrelated reconcile. On failure, the condition stays pending and
	// the next requeue re-tries the sweep.
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
			// NotApplicable is reported as Status=True so that
			// `rdd set running=true containerEngine.name=containerd`
			// stops waiting on ContainerEngineReady — even though the
			// engine controller is not mirroring anything in this
			// backend. UI consumers that expect Container/Image/Volume
			// resources should gate on the Reason as well, not on
			// Status alone. The condition will be renamed when
			// containerd mirroring lands.
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
		if err := r.setEngineCondition(ctx, &app, metav1.ConditionTrue, "Connected", "Container engine synced"); err != nil {
			return ctrl.Result{}, err
		}
		// Fall through so pending spec.state patches and finalizers
		// made during the watcher-down window are processed on this
		// same reconcile, instead of waiting for a later event. The
		// restart-after-crash path would otherwise stall if the
		// ContainerEngineReady condition hadn't changed (setEngineCondition
		// is a no-op) and no mirror watch event follows.
	}

	// reconcileContainerSpecs + processFinalizers each issue one or more
	// List() calls across every mirrored object on every reconcile, and
	// every Container/Image/Volume watch event triggers a reconcile via
	// SetupWithManager below. Cost is therefore O(N) per child event.
	// The long-term fix is to split these into per-resource reconcilers
	// with watch predicates ("deletion timestamp set", "spec.state
	// changed"); until then the sweep runs on every event.
	if err := r.reconcileContainerSpecs(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile container specs: %w", err)
	}
	if err := r.processFinalizers(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process finalizers: %w", err)
	}

	return ctrl.Result{}, nil
}

// startWatcher creates and starts a Docker watcher goroutine.
func (r *EngineReconciler) startWatcher(ctx context.Context) error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	if r.watcher != nil {
		return nil
	}

	w, err := newDockerWatcher(ctx, r.Client, r.reconcileChan)
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

// setEngineCondition updates the ContainerEngineReady condition on the
// App resource. The App controller also writes App.Status.Conditions
// (to mirror LimaVM conditions) and controller-runtime does not
// serialize reconciles across controllers, so a naive Update can race
// and 409. retry.RetryOnConflict plus a re-Get inside the loop is the
// same pattern used elsewhere in this file (see removeMirrorResource,
// deleteAllOfType).
func (r *EngineReconciler) setEngineCondition(ctx context.Context, app *appv1alpha1.App, status metav1.ConditionStatus, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appv1alpha1.App{}
		if err := r.Get(ctx, client.ObjectKey{Name: app.Name}, latest); err != nil {
			// The App can be deleted concurrently with the reconciler
			// (e.g., rdd svc delete mid-reconcile). Treat NotFound as a
			// no-op so we do not propagate a spurious error that would
			// requeue only to be IgnoreNotFound-dropped on the next
			// reconcile.
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
// controller owns. Errors are collected across all four kinds with
// errors.Join so one stuck resource type does not block the rest — the
// remaining types are still swept and the caller requeues on the
// combined error, retrying only what actually failed.
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

// deleteAllOfType lists resources, strips finalizers, and deletes them.
// Finalizer removal uses retry.RetryOnConflict so a stale cache or a
// concurrent Update does not require the caller to requeue. Per-item
// errors are collected so one stuck object does not block the rest of
// the list.
func (r *EngineReconciler) deleteAllOfType(ctx context.Context, list client.ObjectList) error {
	if err := r.List(ctx, list, client.InNamespace(apiNamespace)); err != nil {
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
		// meta.ExtractList strips the TypeMeta from each item (the API
		// server only populates GVK on the top-level list), so
		// obj.GetObjectKind() is empty here. Format the concrete Go
		// type name with %T instead.
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := obj.DeepCopyObject().(client.Object)
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !removeFinalizer(latest, mirrorFinalizer) {
				return nil
			}
			return r.Update(ctx, latest)
		})
		if retryErr != nil {
			errs = append(errs, fmt.Errorf("failed to remove finalizer from %T %s: %w",
				obj, obj.GetName(), retryErr))
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, obj)); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete %T %s: %w",
				obj, obj.GetName(), err))
		}
	}
	return errors.Join(errs...)
}

// reconcileContainerSpecs handles `Container` spec.state changes by
// calling Docker start/stop. Per-Container errors are collected with
// errors.Join and returned so controller-runtime requeues with backoff
// — matching the pattern in processContainerFinalizers. Without the
// return, a failed start/stop would get exactly one attempt (the watch
// event from the original spec.state patch) and then sit forever.
func (r *EngineReconciler) reconcileContainerSpecs(ctx context.Context) error {
	r.watcherMu.Lock()
	w := r.watcher
	r.watcherMu.Unlock()
	if w == nil {
		return nil
	}

	var containers containersv1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(apiNamespace)); err != nil {
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

	// Collect errors across all three types so a stuck Container or
	// Image finalizer does not starve pending Volume finalizers on the
	// same reconcile, matching the per-item pattern used in
	// cleanupMirrorResources.
	return errors.Join(
		r.processContainerFinalizers(ctx, w),
		r.processImageFinalizers(ctx, w),
		r.processVolumeFinalizers(ctx, w),
	)
}

// processContainerFinalizers deletes the Docker-side container for every
// `Container` pending deletion and only strips the mirror finalizer
// when the Docker delete succeeds. Per-item errors are collected so one
// stuck Container does not block the rest; the reconciler retries the
// remaining stuck items on the next reconcile.
func (r *EngineReconciler) processContainerFinalizers(ctx context.Context, w *dockerWatcher) error {
	var containers containersv1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range containers.Items {
		c := &containers.Items[i]
		if c.DeletionTimestamp == nil || !hasFinalizer(c, mirrorFinalizer) {
			continue
		}
		if err := w.deleteContainer(ctx, c.Name); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete container %s from Docker: %w", c.Name, err))
			continue
		}
		// Strip the finalizer with retry-on-conflict so a stale cache
		// does not force a whole-reconcile requeue just to reach the
		// same idempotent deleteContainer call. NotFound is benign: a
		// concurrent Docker destroy event may already have stripped
		// the finalizer and deleted the mirror.
		key := client.ObjectKeyFromObject(c)
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &containersv1alpha1.Container{}
			if err := r.Get(ctx, key, latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			if !removeFinalizer(latest, mirrorFinalizer) {
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
	if err := r.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range images.Items {
		img := &images.Items[i]
		if img.DeletionTimestamp == nil || !hasFinalizer(img, mirrorFinalizer) {
			continue
		}
		// An Image mirror with no engine-populated status has no Docker
		// reference to forward the delete to — either a user created it
		// as a bare skeleton or it landed in the startup race window
		// before applyImage ran. Symmetric with processVolumeFinalizers'
		// empty-Status.Name guard: strip the finalizer and let the
		// Delete proceed.
		if img.Status.ID != "" || img.Status.RepoTag != "" {
			if err := w.deleteImage(ctx, img); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete image %s from Docker: %w", img.Name, err))
				continue
			}
		}
		if removeFinalizer(img, mirrorFinalizer) {
			if err := client.IgnoreNotFound(r.Update(ctx, img)); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove finalizer from Image %s: %w", img.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (r *EngineReconciler) processVolumeFinalizers(ctx context.Context, w *dockerWatcher) error {
	var volumes containersv1alpha1.VolumeList
	if err := r.List(ctx, &volumes, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range volumes.Items {
		v := &volumes.Items[i]
		if v.DeletionTimestamp == nil || !hasFinalizer(v, mirrorFinalizer) {
			continue
		}
		// A Volume mirror with an empty Status.Name has no
		// engine-populated status yet — either a user created it as a
		// bare skeleton or it landed in the startup race window before
		// applyVolume ran. There is no Docker-side name to forward a
		// delete to, so strip the finalizer and let the Delete proceed.
		if v.Status.Name != "" {
			if err := w.deleteVolume(ctx, v.Status.Name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete volume %s from Docker: %w", v.Name, err))
				continue
			}
		}
		if removeFinalizer(v, mirrorFinalizer) {
			if err := client.IgnoreNotFound(r.Update(ctx, v)); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove finalizer from Volume %s: %w", v.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// SetupWithManager sets up the controller with the Manager.
func (r *EngineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconcileChan = make(chan event.GenericEvent, 1)

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

// hasFinalizer checks whether an object has the specified finalizer.
func hasFinalizer(obj metav1.Object, finalizer string) bool {
	for _, f := range obj.GetFinalizers() {
		if f == finalizer {
			return true
		}
	}
	return false
}

// removeFinalizer removes the specified finalizer from an object. Returns true
// if the finalizer was present and removed.
func removeFinalizer(obj metav1.Object, finalizer string) bool {
	finalizers := obj.GetFinalizers()
	for i, f := range finalizers {
		if f == finalizer {
			obj.SetFinalizers(append(finalizers[:i], finalizers[i+1:]...))
			return true
		}
	}
	return false
}
