// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/events"
	dockerclient "github.com/moby/moby/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// dockerWatcher manages a Docker client connection and event stream. It
// performs a full sync on connect and then watches for incremental changes.
type dockerWatcher struct {
	cli *dockerclient.Client
	k8s client.Client

	cancel context.CancelFunc
	done   chan struct{}

	// reconcileChan is used to trigger reconciliation in the engine reconciler.
	reconcileChan chan<- event.GenericEvent
}

// newDockerWatcher creates a Docker client, performs a full sync, and starts
// the event stream watcher goroutine.
func newDockerWatcher(ctx context.Context, k8s client.Client, reconcileChan chan<- event.GenericEvent) (*dockerWatcher, error) {
	socketPath := instance.DockerSocket()
	cli, err := dockerclient.New(
		dockerclient.WithHost("unix://" + socketPath),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify the connection by pinging Docker.
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if _, err := cli.Ping(pingCtx, dockerclient.PingOptions{}); err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to ping Docker: %w", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)

	w := &dockerWatcher{
		cli:           cli,
		k8s:           k8s,
		cancel:        watchCancel,
		done:          make(chan struct{}),
		reconcileChan: reconcileChan,
	}

	// Capture a Docker-relative timestamp before fullSync begins. The
	// run() goroutine passes this to the event stream as the Since
	// filter, so any mutation that happens while fullSync is snapshotting
	// state is replayed once the stream opens. Without this, the window
	// between the List-based snapshot and the event subscription is a
	// blind spot during VM startup.
	since := strconv.FormatInt(time.Now().Unix(), 10)

	if err := w.fullSync(watchCtx); err != nil {
		watchCancel()
		cli.Close()
		return nil, fmt.Errorf("failed to perform initial sync: %w", err)
	}

	go w.run(watchCtx, since)

	return w, nil
}

// stop cancels the watcher goroutine and waits for it to finish.
func (w *dockerWatcher) stop() {
	w.cancel()
	<-w.done
	w.cli.Close()
}

// alive returns true if the watcher goroutine is still running.
func (w *dockerWatcher) alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// run is the main watcher goroutine that processes Docker events. The
// since parameter is the "Since" filter passed to the Docker events
// endpoint, captured before the initial fullSync so events that
// happened during the sync window are replayed once the stream opens.
func (w *dockerWatcher) run(ctx context.Context, since string) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	defer close(w.done)
	// Recover from panics in event handling so an unexpected Docker
	// event shape cannot crash the whole app-controller process. The
	// engine reconciler notices the dead watcher via alive() on the
	// next reconcile and starts a fresh one.
	defer func() {
		if r := recover(); r != nil {
			log.Error(nil, "panic in Docker watcher goroutine", "recovered", r)
		}
	}()

	eventFilter := dockerclient.Filters{}.
		Add("type", string(events.ContainerEventType)).
		Add("type", string(events.ImageEventType)).
		Add("type", string(events.VolumeEventType))

	result := w.cli.Events(ctx, dockerclient.EventsListOptions{
		Since:   since,
		Filters: eventFilter,
	})

	for {
		select {
		case <-ctx.Done():
			log.Info("Docker watcher stopping")
			return
		case err := <-result.Err:
			if ctx.Err() != nil {
				return
			}
			log.Error(err, "Docker event stream error")
			w.enqueueReconcile()
			return
		case msg := <-result.Messages:
			if err := w.handleEvent(ctx, msg); err != nil {
				log.Error(err, "Failed to handle Docker event",
					"type", msg.Type, "action", msg.Action, "actor", msg.Actor.ID)
			}
		}
	}
}

// enqueueReconcile triggers a reconcile in the engine reconciler.
func (w *dockerWatcher) enqueueReconcile() {
	select {
	case w.reconcileChan <- event.GenericEvent{}:
	default:
	}
}

// handleEvent processes a single Docker event.
func (w *dockerWatcher) handleEvent(ctx context.Context, msg events.Message) error {
	switch msg.Type {
	case events.ContainerEventType:
		return w.handleContainerEvent(ctx, msg)
	case events.ImageEventType:
		return w.handleImageEvent(ctx, msg)
	case events.VolumeEventType:
		return w.handleVolumeEvent(ctx, msg)
	default:
		return nil
	}
}

// handleContainerEvent processes container events.
func (w *dockerWatcher) handleContainerEvent(ctx context.Context, msg events.Message) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	switch msg.Action {
	case events.ActionCreate,
		events.ActionStart,
		events.ActionStop,
		events.ActionDie,
		events.ActionPause,
		events.ActionUnPause,
		events.ActionRestart:
		log.V(1).Info("Container event", "action", msg.Action, "id", msg.Actor.ID)
		return w.syncContainer(ctx, msg.Actor.ID)

	case events.ActionDestroy:
		log.V(1).Info("Container destroyed", "id", msg.Actor.ID)
		return w.removeMirrorResource(ctx, &containersv1alpha1.Container{}, msg.Actor.ID)

	default:
		return nil
	}
}

// handleImageEvent processes image events.
func (w *dockerWatcher) handleImageEvent(ctx context.Context, msg events.Message) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	switch msg.Action {
	case events.ActionPull,
		events.ActionImport,
		events.ActionLoad,
		events.ActionTag:
		// Tag events may transition a dangling mirror into a tagged one
		// (or vice versa on untag). reconcileImageByID applies the
		// current tag set and prunes K8s mirrors whose names are no
		// longer in that set, so stale dangling or stale-tag mirrors do
		// not accumulate between watcher restarts.
		log.V(1).Info("Image event", "action", msg.Action, "id", msg.Actor.ID)
		return w.reconcileImageByID(ctx, msg.Actor.ID)

	case events.ActionUnTag:
		// Docker's untag event does not propagate the removed tag name —
		// Actor.ID and Attributes["name"] both carry the image ID hash
		// (see moby daemon/images/image_delete.go). Re-inspect the image
		// and let reconcileImageByID prune any K8s mirrors whose
		// RepoTag is no longer present.
		log.V(1).Info("Image untagged", "id", msg.Actor.ID)
		return w.reconcileImageByID(ctx, msg.Actor.ID)

	case events.ActionDelete:
		log.V(1).Info("Image deleted", "id", msg.Actor.ID)
		return w.removeImagesByID(ctx, msg.Actor.ID)

	default:
		return nil
	}
}

// handleVolumeEvent processes volume events.
func (w *dockerWatcher) handleVolumeEvent(ctx context.Context, msg events.Message) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	switch msg.Action {
	case events.ActionCreate:
		log.V(1).Info("Volume created", "name", msg.Actor.ID)
		return w.syncVolume(ctx, msg.Actor.ID)

	case events.ActionDestroy:
		log.V(1).Info("Volume destroyed", "name", msg.Actor.ID)
		return w.removeMirrorResource(ctx, &containersv1alpha1.Volume{},
			volumeK8sName(msg.Actor.ID))

	default:
		return nil
	}
}

// removeMirrorResource removes the finalizer from a mirror resource and deletes
// it. This is used when Docker has already deleted the object.
//
// The finalizer-strip Update is wrapped in retry.RetryOnConflict to
// survive a stale cache during concurrent reconciles; NotFound means
// the mirror is already gone and is treated as success. Any other
// Update error propagates so the event handler retries on the next
// reconcile instead of stripping the mirror while leaving a stale
// object behind.
//
// The obj parameter is used as an empty object template: callers may
// pass either a zero-valued struct (e.g. &containersv1alpha1.Volume{})
// or a slice element from a prior List; in both cases each retry
// iteration starts from a fresh copy via DeepCopyObject and writes only
// that copy, so caller state is never observed.
func (w *dockerWatcher) removeMirrorResource(ctx context.Context, obj client.Object, name string) error {
	key := client.ObjectKey{Name: name, Namespace: apiNamespace}
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := obj.DeepCopyObject().(client.Object)
		if err := w.k8s.Get(ctx, key, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !removeFinalizer(latest, mirrorFinalizer) {
			return nil
		}
		return w.k8s.Update(ctx, latest)
	})
	if retryErr != nil {
		return fmt.Errorf("failed to remove finalizer from %s: %w", name, retryErr)
	}
	deleteTarget := obj.DeepCopyObject().(client.Object)
	deleteTarget.SetName(name)
	deleteTarget.SetNamespace(apiNamespace)
	return client.IgnoreNotFound(w.k8s.Delete(ctx, deleteTarget))
}

// reconcileContainerState checks if the user set spec.state to "running" or
// "created" and calls Docker start/stop accordingly. The engine creates
// containers with spec.state="unknown", which the reconciler ignores.
func (w *dockerWatcher) reconcileContainerState(ctx context.Context, c *containersv1alpha1.Container) error {
	desired := c.Spec.State
	if desired == containersv1alpha1.ContainerStatusUnknown {
		return nil
	}

	actual := c.Status.Status
	if desired == actual {
		return nil
	}

	log := logf.FromContext(ctx).WithName("docker-watcher")

	// desired != actual is guaranteed by the early return above, so each
	// branch just dispatches to Docker. ContainerStop handles paused /
	// restarting containers natively — filtering by actual == running
	// would silently drop the user's intent in those states. For the
	// symmetric direction (desired=running, actual=paused), Docker
	// rejects ContainerStart with "container is paused, unpause before
	// starting", so dispatch ContainerUnpause explicitly instead.
	switch desired {
	case containersv1alpha1.ContainerStatusRunning:
		if actual == containersv1alpha1.ContainerStatusPaused {
			log.Info("Unpausing container", "id", c.Name)
			_, err := w.cli.ContainerUnpause(ctx, c.Name, dockerclient.ContainerUnpauseOptions{})
			return err
		}
		log.Info("Starting container", "id", c.Name)
		_, err := w.cli.ContainerStart(ctx, c.Name, dockerclient.ContainerStartOptions{})
		return err
	case containersv1alpha1.ContainerStatusCreated:
		log.Info("Stopping container", "id", c.Name)
		_, err := w.cli.ContainerStop(ctx, c.Name, dockerclient.ContainerStopOptions{})
		return err
	}
	return nil
}

// deleteContainer removes a container from Docker. NotFound errors are
// treated as success (the container is already gone); all other errors
// are returned so the caller keeps the mirror finalizer in place and
// retries on the next reconcile.
//
// Force: true matches the asymmetry between container and image
// deletion: deleting a Container mirror through the K8s API expresses
// the user's intent to remove that container, so Docker is instructed
// to stop it first if running. Images, in contrast, use Force: false
// so in-use images are kept and the finalizer retries until the last
// consumer goes away.
func (w *dockerWatcher) deleteContainer(ctx context.Context, id string) error {
	_, err := w.cli.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// deleteImage removes an image from Docker. See deleteContainer for the
// error-handling contract.
func (w *dockerWatcher) deleteImage(ctx context.Context, img *containersv1alpha1.Image) error {
	// Use the tag if available, otherwise the raw image ID.
	ref := img.Status.ID
	if img.Status.RepoTag != "" {
		ref = img.Status.RepoTag
	}
	_, err := w.cli.ImageRemove(ctx, ref, dockerclient.ImageRemoveOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// deleteVolume removes a volume from Docker. See deleteContainer for the
// error-handling contract.
func (w *dockerWatcher) deleteVolume(ctx context.Context, name string) error {
	_, err := w.cli.VolumeRemove(ctx, name, dockerclient.VolumeRemoveOptions{Force: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// fullSync lists all containers, images, and volumes from Docker and creates
// corresponding K8s resources. It also removes stale K8s resources.
func (w *dockerWatcher) fullSync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	log.Info("Starting full sync")

	if err := w.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	var errs []error

	if err := w.syncContainerNamespace(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync container namespace: %w", err))
	}
	if err := w.syncAllContainers(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync containers: %w", err))
	}
	if err := w.syncAllImages(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync images: %w", err))
	}
	if err := w.syncAllVolumes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync volumes: %w", err))
	}

	log.Info("Full sync complete")
	return errors.Join(errs...)
}
