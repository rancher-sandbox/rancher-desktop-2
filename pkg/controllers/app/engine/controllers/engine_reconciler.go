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
	"runtime"
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
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	appName = "app"

	// containerNamespace is the Docker container namespace name;
	// Docker uses a single namespace called "moby".
	containerNamespace = "moby"

	// controllerName is the SSA field owner for engine-controller applies.
	controllerName = "engine-controller"

	// mirrorFinalizer is added to mirror resources so user deletions
	// are forwarded to the container engine before the resource is removed.
	mirrorFinalizer = "engine.rancherdesktop.io/mirror"

	// engineMoby is the App.spec.containerEngine.name value that selects
	// the Docker backend. Containerd has no watcher yet and reports
	// NotApplicable.
	engineMoby = "moby"
)

// engine is the reconciler-facing contract every container-engine
// implementation must satisfy. dockerWatcher is the only current
// implementation; a forthcoming containerd implementation will provide a
// second. Methods that the reconciler does not call (event handlers,
// full-sync internals) stay off the interface.
type engine interface {
	// alive reports whether the engine is still running.
	alive() bool
	// stop terminates the engine and waits for it to finish.
	stop()
	// processContainerAction performs the action requested by the
	// [containersv1alpha1.AnnotationAction] annotation on a Container and
	// records the outcome in status.lastAction.
	processContainerAction(ctx context.Context, c *containersv1alpha1.Container) error
	// deleteContainer removes a container from the engine.
	deleteContainer(ctx context.Context, c *containersv1alpha1.Container) error
	// deleteImage removes an image from the engine.
	deleteImage(ctx context.Context, img *containersv1alpha1.Image) error
	// deleteVolume removes a volume from the engine. Engines without a
	// native volume concept return nil.
	deleteVolume(ctx context.Context, v *containersv1alpha1.Volume) error
}

// EngineReconciler watches the App resource for the Running condition and
// manages an engine watcher goroutine that mirrors engine state into K8s.
//
// The App is a cluster-scoped singleton, so controller-runtime runs at
// most one Reconcile at a time. Only Reconcile and the manager's
// shutdown-hook goroutine (see SetupWithManager) contend for the
// fields below.
type EngineReconciler struct {
	client.Client

	// reconcileChan receives events from the engine watcher goroutine.
	reconcileChan chan event.GenericEvent

	// apiNamespace mirrors App.spec.namespace (immutable). Reconcile
	// populates it before any mirror operation.
	apiNamespace string

	// watcherMu guards r.watcher against the shutdown-hook goroutine's
	// stopWatcher call. Reconcile runs serially (see struct doc), so
	// the lock has no other role.
	watcherMu sync.Mutex
	watcher   engine

	// watcherCtx is the parent context for every engine watcher the
	// reconciler starts. A manager.RunnableFunc cancels it on
	// shutdown, so the watcher outlives individual Reconcile calls
	// but not the manager. Deriving from Reconcile's ctx would leak
	// the engine client: once the manager context cancels, Reconcile
	// no longer runs, and stopWatcher is unreachable from that path.
	watcherCtx    context.Context
	watcherCancel context.CancelFunc

	// contextMu protects contextProbeCancel and contextProbeGen.
	contextMu sync.Mutex
	// contextProbeCancel cancels the in-flight Docker context probe goroutine.
	// It is nil when no probe is running.
	contextProbeCancel context.CancelFunc
	// contextProbeGen is incremented each time a new probe is launched; the
	// goroutine captures its generation at launch and uses it to detect
	// whether it has been superseded.
	contextProbeGen int
	// contextProbeWg is used by removeDockerContext to wait for the probe
	// goroutine to finish before deleting the context directory, ensuring
	// the goroutine cannot write currentContext after the directory is gone.
	contextProbeWg sync.WaitGroup
}

// Reconcile handles App condition changes, Docker watcher lifecycle,
// Container action annotations, and finalizer processing for mirror
// resources.
func (r *EngineReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appv1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	r.apiNamespace = app.GetResourceNamespace()

	running := meta.IsStatusConditionTrue(app.Status.Conditions, appv1alpha1.AppConditionRunning)
	engineIsDocker := app.Spec.ContainerEngine.Name == engineMoby

	// Treat a dead watcher as a transient disconnect and fall through.
	// The watcher's run goroutine closes the Docker client in its own
	// deferred cleanup, so Reconcile only needs to forget the
	// reference. If wantWatcher is still true below, a fresh watcher's
	// fullSync reconciles drift in place — existing mirror resources
	// keep their identity, so downstream clients see no churn. The
	// !wantWatcher branch below handles an actual stop or backend
	// change by sweeping the mirrors.
	var watcherDied bool
	r.watcherMu.Lock()
	watcherRunning := r.watcher != nil
	if watcherRunning && !r.watcher.alive() {
		r.watcher = nil
		watcherRunning = false
		watcherDied = true
	}
	r.watcherMu.Unlock()
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
		} else {
			// The watcher was never started or died on its own (e.g. the VM
			// socket closed). stopWatcher was not called, so clean up the
			// Docker context directly.
			r.removeDockerContext()
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
		current := meta.FindStatusCondition(app.Status.Conditions, appv1alpha1.AppConditionContainerEngineReady)
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
		if err := r.startWatcherAndSync(ctx); err != nil {
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

	// Two passes run per Reconcile: reconcileContainerActions drives
	// Docker actions from the action annotation, and
	// processFinalizers forwards K8s-side deletes to Docker for any
	// mirror still carrying the mirror finalizer.
	//
	// Both passes issue List calls per reconcile, and every
	// Container/Image/Volume watch event triggers a reconcile — so
	// cost is O(N) per child event. The long-term fix is to split
	// these into per-resource reconcilers with watch predicates
	// (deletionTimestamp set, action annotation present).
	//
	// Run both passes unconditionally and join their errors. Early
	// return on actionsErr would stall every mirror's finalizer behind
	// a single stuck container action: the next reconcile would hit
	// the same broken container first and skip finalizers again.
	var actionsErr, finErr error
	if err := r.reconcileContainerActions(ctx); err != nil {
		actionsErr = fmt.Errorf("failed to reconcile container actions: %w", err)
	}
	if err := r.processFinalizers(ctx); err != nil {
		finErr = fmt.Errorf("failed to process finalizers: %w", err)
	}
	return ctrl.Result{}, errors.Join(actionsErr, finErr)
}

// startWatcherAndSync creates a Docker watcher and blocks until its
// initial fullSync has completed; only then does it publish the
// watcher on r.watcher. The watcher inherits watcherCtx, which
// cancels only on manager shutdown, so startWatcherAndSync
// deliberately drops Reconcile's ctx: a future ReconciliationTimeout
// or per-request deadline must not kill the watcher the moment
// Reconcile returns.
func (r *EngineReconciler) startWatcherAndSync(_ context.Context) error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	if r.watcher != nil {
		return nil
	}

	e, err := newDockerWatcher(r.watcherCtx, r.Client, r.apiNamespace, r.reconcileChan)
	if err != nil {
		return err
	}
	r.watcher = e
	r.manageDockerContext(instance.DockerSocket())
	return nil
}

// stopWatcher stops the Docker watcher goroutine and waits for it to finish.
func (r *EngineReconciler) stopWatcher() {
	r.watcherMu.Lock()
	e := r.watcher
	r.watcher = nil
	r.watcherMu.Unlock()

	if e != nil {
		e.stop()
	}
	r.removeDockerContext()
}

// manageDockerContext creates the instance Docker context and, in a goroutine,
// probes the user's current context; if it is absent or unhealthy, switches
// the default to the new context. At most one probe runs at a time.
func (r *EngineReconciler) manageDockerContext(socketPath string) {
	// Docker context management uses unix:// sockets; Windows requires
	// npipe:// which is not yet implemented.
	if runtime.GOOS == "windows" {
		return
	}
	contextName := instance.Name()
	log := logf.FromContext(r.watcherCtx).WithName("docker-context")

	if err := createReplaceDockerContext(contextName, socketPath); err != nil {
		log.Error(err, "Failed to create Docker context", "context", contextName)
		return
	}

	r.contextMu.Lock()
	// Cancel any in-flight probe from a previous transition.
	if r.contextProbeCancel != nil {
		r.contextProbeCancel()
	}
	probeCtx, cancel := context.WithCancel(r.watcherCtx)
	r.contextProbeCancel = cancel
	r.contextProbeGen++
	myGen := r.contextProbeGen
	r.contextMu.Unlock()

	r.contextProbeWg.Add(1)
	go func() {
		defer r.contextProbeWg.Done()
		defer func() {
			r.contextMu.Lock()
			// Clear the cancel func only if we are still the current probe.
			if r.contextProbeGen == myGen {
				r.contextProbeCancel = nil
			}
			r.contextMu.Unlock()
			cancel()
		}()

		current, err := getCurrentDockerContext()
		if err != nil {
			log.Error(err, "Failed to read current Docker context")
		}

		// Probe the current context's host — not our own — to decide
		// whether to promote ours. "default" and empty both mean no
		// working context is set; treat them identically.
		var healthy bool
		if current != "" && current != "default" {
			currentHost, err := getDockerContextHost(current)
			if err != nil {
				log.Error(err, "Failed to resolve current Docker context host", "context", current)
			} else if currentHost != "" {
				healthy = probeDockerContext(probeCtx, currentHost)
			}
		}
		// Guard against writing currentContext after removeDockerContext has
		// already cancelled probeCtx and deleted the context directory.
		if !healthy && probeCtx.Err() == nil {
			if err := setCurrentDockerContext(contextName); err != nil {
				log.Error(err, "Failed to set current Docker context", "context", contextName)
			}
		}
	}()
}

// removeDockerContext cancels any in-flight probe, waits for it to finish,
// then resets the current context if it points at our instance and deletes
// the instance's Docker context directory.
func (r *EngineReconciler) removeDockerContext() {
	if runtime.GOOS == "windows" {
		return
	}
	r.contextMu.Lock()
	if r.contextProbeCancel != nil {
		r.contextProbeCancel()
		r.contextProbeCancel = nil
	}
	r.contextMu.Unlock()

	// Wait for the probe goroutine to finish before deleting the context
	// directory. The goroutine's probeCtx was just cancelled, so Ping will
	// return within dockerContextProbeTimeout (3s) at most.
	r.contextProbeWg.Wait()

	contextName := instance.Name()
	log := logf.FromContext(r.watcherCtx).WithName("docker-context")
	if err := clearCurrentDockerContext(contextName); err != nil {
		log.Error(err, "Failed to clear current Docker context", "context", contextName)
	}
	if err := deleteDockerContext(contextName); err != nil {
		log.Error(err, "Failed to delete Docker context", "context", contextName)
	}
}

// setEngineCondition updates the ContainerEngineReady condition on the App.
func (r *EngineReconciler) setEngineCondition(ctx context.Context, app *appv1alpha1.App, status metav1.ConditionStatus, reason, message string) error {
	// The App controller also writes App.Status.Conditions (to
	// mirror LimaVM conditions), and controller-runtime runs
	// reconciles across controllers in parallel, so a naive Update
	// races and 409s. Retry on conflict with a re-Get.
	//
	// Use client-go Update rather than SSA: ObservedGeneration must
	// reflect the specific generation this call's Get observed, and
	// meta.SetStatusCondition expects the read-modify-write pattern.
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
			Type:               appv1alpha1.AppConditionContainerEngineReady,
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

// reconcileContainerActions processes the AnnotationAction annotation on
// every container that carries one. Per-container errors are joined and
// returned; a patch failure requeues the reconcile so the caller retries.
// Containers without the annotation are skipped.
func (r *EngineReconciler) reconcileContainerActions(ctx context.Context) error {
	r.watcherMu.Lock()
	e := r.watcher
	r.watcherMu.Unlock()
	if e == nil {
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
		if _, ok := c.Annotations[containersv1alpha1.AnnotationAction]; !ok {
			continue
		}
		if err := e.processContainerAction(ctx, c); err != nil {
			errs = append(errs, fmt.Errorf("container %s: %w", c.Name, err))
		}
	}
	return errors.Join(errs...)
}

// processFinalizers handles resources with a deletion timestamp by deleting
// the corresponding Docker object and removing the finalizer.
func (r *EngineReconciler) processFinalizers(ctx context.Context) error {
	r.watcherMu.Lock()
	e := r.watcher
	r.watcherMu.Unlock()
	if e == nil {
		return nil
	}

	// Join errors across all three types so a stuck Container or
	// Image finalizer does not starve pending Volume finalizers.
	return errors.Join(
		r.processContainerFinalizers(ctx, e),
		r.processImageFinalizers(ctx, e),
		r.processVolumeFinalizers(ctx, e),
	)
}

// processContainerFinalizers deletes the Docker-side container for
// every Container pending deletion. The mirror finalizer is only
// stripped when the Docker delete succeeds, so a stuck container keeps
// retrying on later reconciles.
func (r *EngineReconciler) processContainerFinalizers(ctx context.Context, e engine) error {
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
		if err := e.deleteContainer(ctx, c); err != nil {
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

func (r *EngineReconciler) processImageFinalizers(ctx context.Context, e engine) error {
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
		// An Image mirror with empty status.id and status.repoTag has
		// no Docker reference to forward the delete to (bare-skeleton
		// user create, or the startup race before applyImage ran).
		// Strip the finalizer and let the Delete proceed — symmetric
		// with processVolumeFinalizers' empty-status.name guard.
		if img.Status.ID != "" || img.Status.RepoTag != "" {
			if err := e.deleteImage(ctx, img); err != nil {
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

func (r *EngineReconciler) processVolumeFinalizers(ctx context.Context, e engine) error {
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
			if err := e.deleteVolume(ctx, v); err != nil {
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
