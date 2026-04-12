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

	// mirrorFinalizer is added to mirror resources so K8s deletions can be
	// forwarded to Docker before the resource is removed.
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

// Reconcile handles App condition changes, Docker watcher lifecycle, container
// spec.state transitions, and finalizer processing for mirror resources.
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
			_ = r.setEngineCondition(ctx, &app, metav1.ConditionFalse, "ConnectFailed", err.Error())
			return ctrl.Result{}, err
		}
		if err := r.setEngineCondition(ctx, &app, metav1.ConditionTrue, "Connected", "Container engine synced"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
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

// setEngineCondition updates the ContainerEngineReady condition on the App resource.
func (r *EngineReconciler) setEngineCondition(ctx context.Context, app *appv1alpha1.App, status metav1.ConditionStatus, reason, message string) error {
	changed := meta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:               conditionContainerEngineReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: app.Generation,
	})
	if !changed {
		return nil
	}
	return r.Status().Update(ctx, app)
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
		errs = append(errs, fmt.Errorf("failed to delete containers: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.VolumeList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete volumes: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ImageList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete images: %w", err))
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ContainerNamespaceList{}); err != nil {
		errs = append(errs, fmt.Errorf("failed to delete container namespaces: %w", err))
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
		kind := obj.GetObjectKind().GroupVersionKind().Kind

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
			errs = append(errs, fmt.Errorf("failed to remove finalizer from %s/%s: %w",
				kind, obj.GetName(), retryErr))
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, obj)); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete %s/%s: %w",
				kind, obj.GetName(), err))
		}
	}
	return errors.Join(errs...)
}

// reconcileContainerSpecs handles container spec.state changes by calling
// Docker start/stop. Per-container errors are collected with errors.Join
// and returned so controller-runtime requeues with backoff — matching
// the pattern in processContainerFinalizers. Without the return, a
// failed start/stop would get exactly one attempt (the watch event from
// the original spec.state patch) and then sit forever.
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

	if err := r.processContainerFinalizers(ctx, w); err != nil {
		return err
	}
	if err := r.processImageFinalizers(ctx, w); err != nil {
		return err
	}
	return r.processVolumeFinalizers(ctx, w)
}

// processContainerFinalizers deletes the Docker-side container for every
// K8s Container pending deletion and only strips the mirror finalizer
// when the Docker delete succeeds. Per-item errors are collected so one
// stuck container does not block the rest; the reconciler retries the
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
		if removeFinalizer(c, mirrorFinalizer) {
			// NotFound is benign: a concurrent Docker destroy event may
			// already have stripped the finalizer and deleted the
			// mirror between our List and Update.
			if err := client.IgnoreNotFound(r.Update(ctx, c)); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove finalizer from container %s: %w", c.Name, err))
			}
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
		if err := w.deleteImage(ctx, img); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete image %s from Docker: %w", img.Name, err))
			continue
		}
		if removeFinalizer(img, mirrorFinalizer) {
			if err := client.IgnoreNotFound(r.Update(ctx, img)); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove finalizer from image %s: %w", img.Name, err))
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
		if err := w.deleteVolume(ctx, v.Status.Name); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete volume %s from Docker: %w", v.Name, err))
			continue
		}
		if removeFinalizer(v, mirrorFinalizer) {
			if err := client.IgnoreNotFound(r.Update(ctx, v)); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove finalizer from volume %s: %w", v.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

// SetupWithManager sets up the controller with the Manager.
func (r *EngineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconcileChan = make(chan event.GenericEvent, 1)

	// Map any container/image/volume event to a reconcile of the App singleton.
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
