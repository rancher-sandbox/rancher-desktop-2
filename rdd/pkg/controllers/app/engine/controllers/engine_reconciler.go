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
	"io"
	"sync"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
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

	// mirrorFinalizer is added to mirror resources so user deletions
	// are forwarded to the container engine before the resource is removed.
	mirrorFinalizer = "engine.rancherdesktop.io/mirror"

	// engineMoby is the App.spec.containerEngine.name value that selects
	// the Docker backend. Containerd has no watcher yet and reports
	// NotApplicable.
	engineMoby = "moby"
)

// engineRequest is the typed reconcile request for EngineReconciler.
// It carries both the originating kind and the target namespaced name.
type engineRequest struct {
	Kind string
	types.NamespacedName
	types.UID
}

// engineLogOptionsData holds the options for fetching container logs.
type engineLogOptionsData struct {
	tail   string
	follow bool
}

// engineLogOptions is a functional option for fetching container logs.
type engineLogOptions func(*engineLogOptionsData)

// engineLogWithTail returns an engineLogOptions that determines how many lines
// of logs to fetch.
func engineLogWithTail(tail string) engineLogOptions {
	return func(opts *engineLogOptionsData) {
		opts.tail = tail
	}
}

// engineLogWithFollow returns an engineLogOptions that determines whether to
// continue streaming logs after the initial dump.
func engineLogWithFollow(follow bool) engineLogOptions {
	return func(opts *engineLogOptionsData) {
		opts.follow = follow
	}
}

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
	// hasTTY reports whether the container has a TTY allocated.
	hasTTY(ctx context.Context, c *containersv1alpha1.Container) (bool, error)
	// getLogs returns a reader for the container's logs.
	getLogs(ctx context.Context, c *containersv1alpha1.Container, opts ...engineLogOptions) (io.ReadCloser, error)
	// pullImage pulls an image from its repository tag and reports progress.
	pullImage(ctx context.Context, repoTag string, onProgress func(start, current, total int64, units string), onComplete func(error)) error
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

	// apiReader is a direct-to-API-server reader (no cache). Used in
	// deleteAllOfType to guarantee a consistent view of mirror resources
	// at cleanup time, even if the informer cache hasn't caught up yet.
	apiReader client.Reader

	// reconcileAppChan receives events from the engine watcher goroutine.
	reconcileAppChan chan event.GenericEvent
	// reconcileImagePullRequestChan triggers when the engine is connected.
	reconcileImagePullRequestChan chan event.GenericEvent

	// apiNamespace mirrors App.spec.namespace (immutable). Reconcile
	// populates it before any mirror operation.  Must be accessed while holding
	// engineMu.
	apiNamespace string

	// engineMu guards r.engine and r.apiNamespace; note that this may be held for
	// a long time during initialization.
	engineMu sync.Mutex
	engine   engine

	// watcherCtx is the parent context for every engine watcher the
	// reconciler starts. A manager.RunnableFunc cancels it on
	// shutdown, so the watcher outlives individual Reconcile calls
	// but not the manager. Deriving from Reconcile's ctx would leak
	// the engine client: once the manager context cancels, Reconcile
	// no longer runs, and stopWatcher is unreachable from that path.
	watcherCtx    context.Context
	watcherCancel context.CancelFunc

	// contextMu protects contextProbeCancel.
	contextMu sync.Mutex
	// contextProbeCancel cancels the in-flight Docker context probe goroutine.
	// It is nil when no probe is running.
	contextProbeCancel context.CancelFunc
	// contextProbeWg is used by removeDockerContext to wait for the probe
	// goroutine to finish before deleting the context directory, ensuring
	// the goroutine cannot write currentContext after the directory is gone.
	contextProbeWg sync.WaitGroup

	// imagePullRequestMu guards imagePullRequestState
	imagePullRequestMu sync.Mutex
	// imagePullRequestState maps image pull request UIDs to their state.
	imagePullRequestState map[types.UID]imagePullRequestState
}

// Reconcile dispatches requests by source kind.
func (r *EngineReconciler) Reconcile(ctx context.Context, req engineRequest) (ctrl.Result, error) {
	switch req.Kind {
	case appv1alpha1.AppKind:
		var app appv1alpha1.App
		if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return r.reconcileWatcher(ctx, &app)
	case containersv1alpha1.ImagePullRequestKind:
		return r.reconcileImagePullRequest(ctx, req)
	default:
		logf.FromContext(ctx).Error(
			errors.New("unsupported reconcile request kind"),
			"Ignoring reconcile request",
			"kind", req.Kind,
			"name", req.Name,
			"namespace", req.Namespace,
		)
		return ctrl.Result{}, nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *EngineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.reconcileAppChan = make(chan event.GenericEvent, 1)
	r.reconcileImagePullRequestChan = make(chan event.GenericEvent, 1)
	r.apiReader = mgr.GetAPIReader()
	r.imagePullRequestState = make(map[types.UID]imagePullRequestState)

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

	// When an ImagePullRequest changes, enqueue a reconcile request for that object.
	enqueueRequestsForImagePullRequest := handler.TypedEnqueueRequestsFromMapFunc(
		func(_ context.Context, obj client.Object) []engineRequest {
			return []engineRequest{{
				Kind:           containersv1alpha1.ImagePullRequestKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
				UID:            obj.GetUID(),
			}}
		})
	// When an App, Container, Image, Volume, or ContainerNamespace changes,
	// enqueue a reconcile request for the App so the watcher can update.
	enqueueRequestsForApp := handler.TypedEnqueueRequestsFromMapFunc(
		func(_ context.Context, _ client.Object) []engineRequest {
			return []engineRequest{{
				Kind:           appv1alpha1.AppKind,
				NamespacedName: types.NamespacedName{Name: appName},
			}}
		})
	// enqueueRawRequest returns a handler that enqueues a reconcile request for
	// the specified kind.
	enqueueRawRequest := func(kind string) handler.TypedEventHandler[client.Object, engineRequest] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, _ client.Object) []engineRequest {
			return []engineRequest{{
				Kind:           kind,
				NamespacedName: types.NamespacedName{Name: appName},
			}}
		})
	}

	return builder.TypedControllerManagedBy[engineRequest](mgr).
		Named("engine-reconciler").
		WatchesRawSource(source.TypedChannel(r.reconcileAppChan, enqueueRawRequest(appv1alpha1.AppKind))).
		WatchesRawSource(source.TypedChannel(r.reconcileImagePullRequestChan, enqueueRawRequest(containersv1alpha1.ImagePullRequestKind))).
		Watches(&appv1alpha1.App{}, enqueueRequestsForApp).
		Watches(&containersv1alpha1.Container{}, enqueueRequestsForApp).
		Watches(&containersv1alpha1.Image{}, enqueueRequestsForApp).
		Watches(&containersv1alpha1.ImagePullRequest{}, enqueueRequestsForImagePullRequest).
		Watches(&containersv1alpha1.Volume{}, enqueueRequestsForApp).
		Watches(&containersv1alpha1.ContainerNamespace{}, enqueueRequestsForApp).
		Complete(r)
}
