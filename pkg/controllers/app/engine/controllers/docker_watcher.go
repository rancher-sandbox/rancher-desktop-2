// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	dockerclient "github.com/moby/moby/client"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// dockerWatcher manages a Docker client connection and event stream. It
// performs a full sync on connect and then watches for incremental changes.
type dockerWatcher struct {
	cli    *dockerclient.Client
	k8s    client.Client
	scheme *runtime.Scheme

	cancel context.CancelFunc
	done   chan struct{}

	// reconcileChan is used to trigger reconciliation in the engine reconciler.
	reconcileChan chan<- event.GenericEvent
}

// newDockerWatcher creates a Docker client, performs a full sync, and starts
// the event stream watcher goroutine.
func newDockerWatcher(ctx context.Context, k8s client.Client, scheme *runtime.Scheme, reconcileChan chan<- event.GenericEvent) (*dockerWatcher, error) {
	socketPath := instance.DockerSocket()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("unix://"+socketPath),
		dockerclient.WithAPIVersionNegotiation(),
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
		scheme:        scheme,
		cancel:        watchCancel,
		done:          make(chan struct{}),
		reconcileChan: reconcileChan,
	}

	// Perform initial full sync before starting the event stream.
	if err := w.fullSync(watchCtx); err != nil {
		watchCancel()
		cli.Close()
		return nil, fmt.Errorf("failed to perform initial sync: %w", err)
	}

	go w.run(watchCtx)

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

// run is the main watcher goroutine that processes Docker events.
func (w *dockerWatcher) run(ctx context.Context) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	defer close(w.done)

	eventFilter := dockerclient.Filters{}.
		Add("type", string(events.ContainerEventType)).
		Add("type", string(events.ImageEventType)).
		Add("type", string(events.VolumeEventType))

	result := w.cli.Events(ctx, dockerclient.EventsListOptions{
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
		log.V(1).Info("Image event", "action", msg.Action, "id", msg.Actor.ID)
		return w.syncImage(ctx, msg.Actor.ID)

	case events.ActionUnTag:
		log.V(1).Info("Image untagged", "id", msg.Actor.ID)
		return w.removeImageByTag(ctx, msg.Actor.ID)

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
			sanitizeKubernetesObjectName(msg.Actor.ID))

	default:
		return nil
	}
}

// removeMirrorResource removes the finalizer from a mirror resource and deletes
// it. This is used when Docker has already deleted the object.
func (w *dockerWatcher) removeMirrorResource(ctx context.Context, obj client.Object, name string) error {
	key := client.ObjectKey{Name: name, Namespace: apiNamespace}
	if err := w.k8s.Get(ctx, key, obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if removeFinalizer(obj, mirrorFinalizer) {
		if err := w.k8s.Update(ctx, obj); err != nil {
			return client.IgnoreNotFound(err)
		}
	}
	return client.IgnoreNotFound(w.k8s.Delete(ctx, obj))
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

	switch desired {
	case containersv1alpha1.ContainerStatusRunning:
		if actual != containersv1alpha1.ContainerStatusRunning {
			log.Info("Starting container", "id", c.Name)
			_, err := w.cli.ContainerStart(ctx, c.Name, dockerclient.ContainerStartOptions{})
			return err
		}
	case containersv1alpha1.ContainerStatusCreated:
		if actual == containersv1alpha1.ContainerStatusRunning {
			log.Info("Stopping container", "id", c.Name)
			_, err := w.cli.ContainerStop(ctx, c.Name, dockerclient.ContainerStopOptions{})
			return err
		}
	}
	return nil
}

// deleteContainer removes a container from Docker. Errors are logged but not
// returned, since the container may already be gone.
func (w *dockerWatcher) deleteContainer(ctx context.Context, id string) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	_, err := w.cli.ContainerRemove(ctx, id, dockerclient.ContainerRemoveOptions{Force: true})
	if err != nil {
		log.V(1).Info("Failed to remove container from Docker (may already be gone)",
			"id", id, "error", err)
	}
}

// deleteImage removes an image from Docker.
func (w *dockerWatcher) deleteImage(ctx context.Context, img *containersv1alpha1.Image) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	// Use the tag if available, otherwise the raw image ID.
	ref := img.Status.ID
	if img.Status.RepoTag != "" {
		ref = img.Status.RepoTag
	}
	_, err := w.cli.ImageRemove(ctx, ref, dockerclient.ImageRemoveOptions{})
	if err != nil {
		log.V(1).Info("Failed to remove image from Docker (may already be gone)",
			"ref", ref, "error", err)
	}
}

// deleteVolume removes a volume from Docker.
func (w *dockerWatcher) deleteVolume(ctx context.Context, name string) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	_, err := w.cli.VolumeRemove(ctx, name, dockerclient.VolumeRemoveOptions{Force: true})
	if err != nil {
		log.V(1).Info("Failed to remove volume from Docker (may already be gone)",
			"name", name, "error", err)
	}
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
