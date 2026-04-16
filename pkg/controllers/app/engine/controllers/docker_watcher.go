// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/events"
	dockerclient "github.com/moby/moby/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

	// apiNamespace is the Kubernetes namespace where mirror resources live.
	apiNamespace string

	cancel context.CancelFunc
	done   chan struct{}

	// reconcileChan is used to trigger reconciliation in the engine reconciler.
	reconcileChan chan<- event.GenericEvent
}

// newDockerWatcher creates a Docker client, performs a full sync, and starts
// the event stream watcher goroutine.
func newDockerWatcher(ctx context.Context, k8s client.Client, apiNamespace string, reconcileChan chan<- event.GenericEvent) (*dockerWatcher, error) {
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
		apiNamespace:  apiNamespace,
		cancel:        watchCancel,
		done:          make(chan struct{}),
		reconcileChan: reconcileChan,
	}

	// Capture "Since" before fullSync so events fired during the snapshot
	// window are replayed once the stream opens. Use the daemon's clock
	// (Info.SystemTime): the daemon evaluates Since against its own
	// clock, and host/guest skew would silently drop events inside the
	// skew window. On Info failure, fall back to a biased host clock —
	// fullSync is idempotent, so replaying a few extra minutes is safe.
	since, err := dockerEventsSince(ctx, cli)
	if err != nil {
		log := logf.FromContext(ctx).WithName("docker-watcher")
		log.V(1).Info("Failed to query Docker daemon time, falling back to biased host clock",
			"error", err)
		since = strconv.FormatInt(time.Now().Add(-2*time.Minute).Unix(), 10)
	}

	if err := w.fullSync(watchCtx); err != nil {
		watchCancel()
		cli.Close()
		return nil, fmt.Errorf("failed to perform initial sync: %w", err)
	}

	go w.run(watchCtx, since)

	return w, nil
}

// dockerEventsSince returns a Docker events "Since" timestamp anchored
// on the daemon's own clock, avoiding host/guest clock skew.
func dockerEventsSince(ctx context.Context, cli *dockerclient.Client) (string, error) {
	infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := cli.Info(infoCtx, dockerclient.InfoOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to query Docker info: %w", err)
	}
	if result.Info.SystemTime == "" {
		return "", errors.New("missing SystemTime in Docker daemon info")
	}
	t, err := time.Parse(time.RFC3339Nano, result.Info.SystemTime)
	if err != nil {
		return "", fmt.Errorf("failed to parse Docker SystemTime %q: %w", result.Info.SystemTime, err)
	}
	return strconv.FormatInt(t.Unix(), 10), nil
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

// run is the main watcher goroutine. since is the Docker events
// "Since" filter captured before fullSync.
func (w *dockerWatcher) run(ctx context.Context, since string) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	// Defers fire LIFO: close(w.done) runs first, then enqueueReconcile.
	// The order matters — if enqueueReconcile ran before w.done closed,
	// the reconciler could wake, see alive()==true on the about-to-exit
	// goroutine, and skip the restart.
	defer w.enqueueReconcile()
	defer close(w.done)
	// Keep a bad event shape from crashing the whole app-controller.
	defer func() {
		if r := recover(); r != nil {
			log.Error(nil, "panic in Docker watcher goroutine",
				"recovered", r, "stack", string(debug.Stack()))
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
			return
		case msg, ok := <-result.Messages:
			if !ok {
				// Daemon closed the stream without writing result.Err;
				// the deferred enqueueReconcile will wake the reconciler.
				log.Info("Docker event stream closed")
				return
			}
			// Transient handleEvent errors (API 503, SSA conflict past
			// its internal retry) are logged and dropped. Container
			// events self-heal on the next state change; image pull and
			// volume create events fire once, so a dropped apply leaves
			// the mirror missing until the next full resync. A periodic
			// fullSync tick is deferred until the rest of the mirroring
			// work lands.
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
		events.ActionRestart,
		events.ActionRename:
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
		// Tag events can promote a dangling mirror to a tagged one.
		// reconcileImageByID re-applies the current tag set and prunes
		// mirrors whose names are no longer in it.
		log.V(1).Info("Image event", "action", msg.Action, "id", msg.Actor.ID)
		return w.reconcileImageByID(ctx, msg.Actor.ID)

	case events.ActionUnTag:
		// Docker's untag event does not carry the removed tag name —
		// Actor.ID and Attributes["name"] both hold the image ID (see
		// moby daemon/images/image_delete.go). Re-inspect and let
		// reconcileImageByID prune mirrors whose RepoTag is gone.
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
			volumeMirrorName(msg.Actor.ID))

	default:
		return nil
	}
}

// removeMirrorResource strips the finalizer from a mirror resource and
// deletes it, used when Docker has already deleted the underlying
// object. Update retries on conflict to survive a stale cache;
// NotFound counts as success. obj is a template: one DeepCopyObject
// carries name and apiNamespace through both the retry's Get target
// (each Get overwrites its contents) and the final Delete (which keys
// off name+namespace).
func (w *dockerWatcher) removeMirrorResource(ctx context.Context, obj client.Object, name string) error {
	latest := obj.DeepCopyObject().(client.Object)
	latest.SetName(name)
	latest.SetNamespace(w.apiNamespace)
	key := client.ObjectKeyFromObject(latest)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := w.k8s.Get(ctx, key, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
			return nil
		}
		return w.k8s.Update(ctx, latest)
	})
	if retryErr != nil {
		return fmt.Errorf("failed to remove finalizer from %s: %w", name, retryErr)
	}
	return client.IgnoreNotFound(w.k8s.Delete(ctx, latest))
}

// reconcileContainerState dispatches the Docker action that bridges
// the Container's spec.state and its observed status. spec.state of
// "unknown" is a no-op.
//
// State matrix (rows=desired, columns=actual). The webhook restricts
// desired to {unknown, created, running}; actual can be any CRD enum
// value.
//
//	desired \ actual  | running | paused   | created | exited | dead | pausing/restarting | removing | unknown
//	------------------+---------+----------+---------+--------+------+--------------------+----------+--------
//	running           | nil     | unpause  | start   | start  | start| start              | start    | start
//	created           | stop    | stop     | nil     | nil    | nil  | stop               | stop     | stop
//
// ContainerStop handles paused and restarting containers natively, so
// the created branch always dispatches Stop from any non-stopped
// state. The running branch handles paused explicitly because Docker
// rejects ContainerStart on a paused container.
func (w *dockerWatcher) reconcileContainerState(ctx context.Context, c *containersv1alpha1.Container) error {
	desired := c.Spec.State
	if desired == containersv1alpha1.ContainerStatusUnknown {
		return nil
	}

	actual := c.Status.Status
	log := logf.FromContext(ctx).WithName("docker-watcher")

	switch desired {
	case containersv1alpha1.ContainerStatusRunning:
		if actual == containersv1alpha1.ContainerStatusRunning {
			return nil
		}
		if actual == containersv1alpha1.ContainerStatusPaused {
			log.Info("Unpausing container", "id", c.Name)
			_, err := w.cli.ContainerUnpause(ctx, c.Name, dockerclient.ContainerUnpauseOptions{})
			return err
		}
		log.Info("Starting container", "id", c.Name)
		_, err := w.cli.ContainerStart(ctx, c.Name, dockerclient.ContainerStartOptions{})
		return err
	case containersv1alpha1.ContainerStatusCreated:
		if isStopped(actual) {
			return nil
		}
		log.Info("Stopping container", "id", c.Name)
		_, err := w.cli.ContainerStop(ctx, c.Name, dockerclient.ContainerStopOptions{})
		return err
	}
	return nil
}

// isStopped reports whether a container status is a terminal
// non-running state that satisfies a desired state of "created".
// Redispatching ContainerStop against any of these is wasted work.
func isStopped(s containersv1alpha1.ContainerStatusValue) bool {
	switch s {
	case containersv1alpha1.ContainerStatusCreated,
		containersv1alpha1.ContainerStatusExited,
		containersv1alpha1.ContainerStatusDead:
		return true
	}
	return false
}

// deleteContainer removes a container from Docker. NotFound is treated
// as success; other errors propagate so the caller keeps the finalizer
// and retries.
//
// Force: true expresses user intent: a K8s-side delete of a Container
// mirror means "remove this container", so Docker stops it first if
// running. Images use Force: false so in-use images are kept and the
// finalizer retries until the last consumer goes away.
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

// fullSync lists all containers, images, and volumes from Docker and
// creates corresponding mirror resources. It also removes stale mirrors.
// The App controller creates and deletes the namespace, so fullSync can assume it exists.
func (w *dockerWatcher) fullSync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	log.Info("Starting full sync")

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
