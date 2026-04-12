// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the engine reconciler, which mirrors Docker
// engine state into Kubernetes resources.
package controllers

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
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
)

// engineEvent is sent from the Docker watcher goroutine to the reconciler.
type engineEvent struct {
	// connected is true when the Docker client first connects and full sync
	// is complete, false when the connection is lost.
	connected bool
}

// EngineReconciler watches the App resource for the Running condition and
// manages a Docker watcher goroutine that mirrors engine state into K8s.
type EngineReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// reconcileChan receives events from the Docker watcher goroutine.
	reconcileChan chan event.GenericEvent

	// watcherMu protects watcher state.
	watcherMu sync.Mutex
	watcher   *dockerWatcher
}

func (r *EngineReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appv1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	running := meta.IsStatusConditionTrue(app.Status.Conditions, "Running")

	r.watcherMu.Lock()
	watcherRunning := r.watcher != nil
	// Detect a watcher that died (event stream error). Clean it up so the
	// next branch restarts it.
	if watcherRunning && !r.watcher.alive() {
		log.Info("Docker watcher died, cleaning up")
		r.watcher.stop()
		r.watcher = nil
		watcherRunning = false
	}
	r.watcherMu.Unlock()

	if running && !watcherRunning {
		log.Info("App is running, starting Docker watcher")
		if err := r.startWatcher(ctx); err != nil {
			log.Error(err, "Failed to start Docker watcher")
			_ = r.setEngineCondition(ctx, &app, metav1.ConditionFalse, "ConnectFailed", err.Error())
			return ctrl.Result{}, err
		}
		if err := r.setEngineCondition(ctx, &app, metav1.ConditionTrue, "Connected", "Docker engine synced"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !running && watcherRunning {
		log.Info("App is not running, stopping Docker watcher")
		r.stopWatcher()
		if err := r.setEngineCondition(ctx, &app, metav1.ConditionFalse, "Disconnected", "Docker engine not available"); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.cleanupMirrorResources(ctx); err != nil {
			log.Error(err, "Failed to clean up mirror resources")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle container spec changes (state transitions).
	if running {
		if err := r.reconcileContainerSpecs(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile container specs: %w", err)
		}
	}

	// Handle finalizer processing for resources being deleted.
	if running {
		if err := r.processFinalizers(ctx); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to process finalizers: %w", err)
		}
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

	w, err := newDockerWatcher(ctx, r.Client, r.Scheme, r.reconcileChan)
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

// cleanupMirrorResources removes all mirror resources (finalizers stripped first).
func (r *EngineReconciler) cleanupMirrorResources(ctx context.Context) error {
	log := logf.FromContext(ctx)
	log.Info("Cleaning up all mirror resources")

	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ContainerList{}); err != nil {
		return fmt.Errorf("failed to delete containers: %w", err)
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.VolumeList{}); err != nil {
		return fmt.Errorf("failed to delete volumes: %w", err)
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ImageList{}); err != nil {
		return fmt.Errorf("failed to delete images: %w", err)
	}
	if err := r.deleteAllOfType(ctx, &containersv1alpha1.ContainerNamespaceList{}); err != nil {
		return fmt.Errorf("failed to delete container namespaces: %w", err)
	}
	return nil
}

// deleteAllOfType lists resources, strips finalizers, and deletes them.
func (r *EngineReconciler) deleteAllOfType(ctx context.Context, list client.ObjectList) error {
	if err := r.List(ctx, list, client.InNamespace(apiNamespace)); err != nil {
		return err
	}

	items, err := meta.ExtractList(list)
	if err != nil {
		return err
	}

	for _, item := range items {
		obj := item.(client.Object)
		if removeFinalizer(obj, mirrorFinalizer) {
			if err := r.Update(ctx, obj); err != nil {
				return fmt.Errorf("failed to remove finalizer from %s/%s: %w",
					obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
			}
		}
		if err := r.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete %s/%s: %w",
				obj.GetObjectKind().GroupVersionKind().Kind, obj.GetName(), err)
		}
	}
	return nil
}

// reconcileContainerSpecs handles container spec.state changes by calling
// Docker start/stop.
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

	for i := range containers.Items {
		c := &containers.Items[i]
		if c.DeletionTimestamp != nil {
			continue
		}
		if err := w.reconcileContainerState(ctx, c); err != nil {
			logf.FromContext(ctx).Error(err, "Failed to reconcile container state",
				"container", c.Name)
		}
	}
	return nil
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

func (r *EngineReconciler) processContainerFinalizers(ctx context.Context, w *dockerWatcher) error {
	var containers containersv1alpha1.ContainerList
	if err := r.List(ctx, &containers, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	for i := range containers.Items {
		c := &containers.Items[i]
		if c.DeletionTimestamp == nil || !hasFinalizer(c, mirrorFinalizer) {
			continue
		}
		w.deleteContainer(ctx, c.Name)
		if removeFinalizer(c, mirrorFinalizer) {
			if err := r.Update(ctx, c); err != nil {
				return fmt.Errorf("failed to remove finalizer from container %s: %w", c.Name, err)
			}
		}
	}
	return nil
}

func (r *EngineReconciler) processImageFinalizers(ctx context.Context, w *dockerWatcher) error {
	var images containersv1alpha1.ImageList
	if err := r.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	for i := range images.Items {
img := &images.Items[i]
		if img.DeletionTimestamp == nil || !hasFinalizer(img, mirrorFinalizer) {
			continue
		}
		w.deleteImage(ctx, img)
		if removeFinalizer(img, mirrorFinalizer) {
			if err := r.Update(ctx, img); err != nil {
				return fmt.Errorf("failed to remove finalizer from image %s: %w", img.Name, err)
			}
		}
	}
	return nil
}

func (r *EngineReconciler) processVolumeFinalizers(ctx context.Context, w *dockerWatcher) error {
	var volumes containersv1alpha1.VolumeList
	if err := r.List(ctx, &volumes, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	for i := range volumes.Items {
		v := &volumes.Items[i]
		if v.DeletionTimestamp == nil || !hasFinalizer(v, mirrorFinalizer) {
			continue
		}
		w.deleteVolume(ctx, v.Status.Name)
		if removeFinalizer(v, mirrorFinalizer) {
			if err := r.Update(ctx, v); err != nil {
				return fmt.Errorf("failed to remove finalizer from volume %s: %w", v.Name, err)
			}
		}
	}
	return nil
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
