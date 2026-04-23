// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/containerd/errdefs"
	"github.com/go-logr/logr"
	"github.com/moby/moby/api/types/events"
	mobyclient "github.com/moby/moby/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

var _ engine = (*dockerWatcher)(nil)

// dockerWatcher manages a Docker client connection and event stream. It
// performs a full sync on connect and then watches for incremental changes.
type dockerWatcher struct {
	cli *mobyclient.Client
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
	cli, err := mobyclient.New(
		mobyclient.WithHost("unix://" + socketPath),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Verify the connection by pinging Docker.
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if _, err := cli.Ping(pingCtx, mobyclient.PingOptions{}); err != nil {
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
func dockerEventsSince(ctx context.Context, cli *mobyclient.Client) (string, error) {
	infoCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := cli.Info(infoCtx, mobyclient.InfoOptions{})
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
// run's deferred cleanup closes the Docker client; stop only signals
// the goroutine and blocks until it exits.
func (w *dockerWatcher) stop() {
	w.cancel()
	<-w.done
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
//
// run owns the Docker client's lifetime: it closes cli before
// returning, so a caller that observes alive()==false can drop its
// reference to the watcher without a separate cleanup step.
func (w *dockerWatcher) run(ctx context.Context, since string) {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	// Defers fire LIFO, giving this exit sequence:
	//
	//   1. close(w.done)       — alive() now returns false
	//   2. w.cli.Close()       — Docker client released
	//   3. w.enqueueReconcile() — reconciler wakes and sees !alive()
	//
	// The order matters: if enqueueReconcile ran before w.done closed,
	// the reconciler could wake, see alive()==true on the about-to-exit
	// goroutine, and skip the restart. Closing cli between done and
	// enqueue means the reconciler observes the dead watcher only
	// after its client has been released.
	defer w.enqueueReconcile()
	defer w.cli.Close()
	defer close(w.done)
	// Keep a bad event shape from crashing the whole app-controller.
	defer func() {
		if r := recover(); r != nil {
			log.Error(nil, "panic in Docker watcher goroutine",
				"recovered", r, "stack", string(debug.Stack()))
		}
	}()

	eventFilter := mobyclient.Filters{}.
		Add("type", string(events.ContainerEventType)).
		Add("type", string(events.ImageEventType)).
		Add("type", string(events.VolumeEventType))

	result := w.cli.Events(ctx, mobyclient.EventsListOptions{
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
			// the mirror missing until the next full resync.
			//
			// TODO: add a periodic fullSync tick so dropped image/volume
			// events self-heal without waiting for a watcher restart.
			if err := w.handleEvent(ctx, msg); err != nil {
				log.Error(err, "Failed to handle Docker event",
					"type", msg.Type, "action", msg.Action, "actor", msg.Actor.ID)
			}
		}
	}
}

// enqueueReconcile wakes the engine reconciler. The channel has a
// buffer of one, so enqueueReconcile is a no-op when a reconcile is
// already queued — the reconciler will pick up the current watcher
// state when it runs.
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

// processContainerAction handles a container carrying the AnnotationAction
// annotation. It dispatches the Docker call, records the outcome in
// status.lastAction, and then removes the annotation.
//
// The Docker call runs before the status and metadata patches so that a
// crash mid-flight leaves the annotation in place and the next reconcile
// replays the action. Start, stop, pause, and unpause are idempotent
// against a container already in the target state, so replay is safe.
// Restart has no target state to match: a replay sends SIGTERM and waits
// the grace period a second time, which the controller cannot distinguish
// from a deliberate re-request.
func (w *dockerWatcher) processContainerAction(ctx context.Context, c *containersv1alpha1.Container) error {
	raw, ok := c.Annotations[containersv1alpha1.AnnotationAction]
	if !ok {
		return nil
	}

	log := logf.FromContext(ctx).WithName("docker-watcher")
	action := containersv1alpha1.ContainerAction(raw)
	observedAt := metav1.Now()

	// The webhook rejects invalid action values, but one written while the
	// webhook is offline can still reach storage. Drop such values here;
	// otherwise the CRD enum rejects the status.lastAction write, the
	// annotation stays in place, and every reconcile retries forever.
	if !action.IsValid() {
		log.Info("Dropping invalid container action annotation", "id", c.Name, "action", raw)
		return w.removeActionAnnotation(ctx, c, raw)
	}

	dockerErr := w.dispatchContainerAction(ctx, log, c.Name, action)

	lastAction := &containersv1alpha1.ContainerLastAction{
		Action:      action,
		ObservedAt:  observedAt,
		CompletedAt: metav1.Now(),
	}
	if dockerErr == nil {
		lastAction.State = containersv1alpha1.ContainerActionSucceeded
	} else {
		lastAction.State = containersv1alpha1.ContainerActionFailed
		lastAction.Error = dockerErr.Error()
		log.Info("Container action failed", "id", c.Name, "action", action, "error", dockerErr)
	}

	latest, err := w.patchContainerLastAction(ctx, c.Name, lastAction)
	if err != nil {
		return fmt.Errorf("failed to patch lastAction for %s: %w", c.Name, err)
	}
	if latest == nil {
		// Mirror was deleted between dispatch and the status patch; nothing
		// left to clean up.
		return nil
	}
	if err := w.removeActionAnnotation(ctx, latest, raw); err != nil {
		return fmt.Errorf("failed to remove action annotation for %s: %w", c.Name, err)
	}
	return nil
}

// dispatchContainerAction executes the Docker call for a single action. The
// caller pre-validates the action name; the default branch triggers only
// when a new ContainerAction value is added to the type but not to the switch.
//
// Pause and unpause pre-check the current Docker state and return nil when
// the container is already in the target state. Two reconcile ticks can read
// the same action annotation through the informer cache before the cache
// sees the removal that follows the first dispatch — the pre-check keeps
// the second dispatch from flipping lastAction to Failed. Start, stop, and
// restart need no pre-check: Docker itself returns 304 Not Modified for
// start/stop on a container already in the target state, and a duplicate
// restart is a harmless double-restart.
func (w *dockerWatcher) dispatchContainerAction(ctx context.Context, log logr.Logger, id string, action containersv1alpha1.ContainerAction) error {
	switch action {
	case containersv1alpha1.ContainerActionStart:
		log.Info("Starting container", "id", id)
		_, err := w.cli.ContainerStart(ctx, id, mobyclient.ContainerStartOptions{})
		return err
	case containersv1alpha1.ContainerActionStop:
		log.Info("Stopping container", "id", id)
		_, err := w.cli.ContainerStop(ctx, id, mobyclient.ContainerStopOptions{})
		return err
	case containersv1alpha1.ContainerActionPause:
		_, paused, err := w.containerRunState(ctx, id)
		if err != nil {
			return err
		}
		if paused {
			return nil
		}
		log.Info("Pausing container", "id", id)
		_, err = w.cli.ContainerPause(ctx, id, mobyclient.ContainerPauseOptions{})
		return err
	case containersv1alpha1.ContainerActionUnpause:
		running, paused, err := w.containerRunState(ctx, id)
		if err != nil {
			return err
		}
		if !running {
			return fmt.Errorf("cannot unpause container %s: not running", id)
		}
		if !paused {
			return nil
		}
		log.Info("Unpausing container", "id", id)
		_, err = w.cli.ContainerUnpause(ctx, id, mobyclient.ContainerUnpauseOptions{})
		return err
	case containersv1alpha1.ContainerActionRestart:
		log.Info("Restarting container", "id", id)
		_, err := w.cli.ContainerRestart(ctx, id, mobyclient.ContainerRestartOptions{})
		return err
	}
	return fmt.Errorf("unknown container action %q", action)
}

func (w *dockerWatcher) hasTTY(ctx context.Context, c *containersv1alpha1.Container) (bool, error) {
	inspect, err := w.cli.ContainerInspect(ctx, c.Name, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return false, err
	}

	return inspect.Container.Config != nil && inspect.Container.Config.Tty, nil
}

func (w *dockerWatcher) getLogs(ctx context.Context, c *containersv1alpha1.Container, opts ...engineLogOptions) (reader io.ReadCloser, err error) {
	options := &engineLogOptionsData{
		tail:   "", // Defaults to all logs.
		follow: true,
	}
	for _, opt := range opts {
		opt(options)
	}

	return w.cli.ContainerLogs(ctx, c.Name, mobyclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     options.follow,
		Tail:       options.tail,
	})
}

// containerRunState reports whether Docker currently shows the container as
// running and as paused. Pause uses paused to skip a no-op dispatch; unpause
// also checks running so that unpause on a stopped container reports a
// failure instead of a silent success.
func (w *dockerWatcher) containerRunState(ctx context.Context, id string) (running, paused bool, err error) {
	inspect, err := w.cli.ContainerInspect(ctx, id, mobyclient.ContainerInspectOptions{})
	if err != nil {
		return false, false, err
	}
	return inspect.Container.State.Running, inspect.Container.State.Paused, nil
}

// patchContainerLastAction writes status.lastAction with retry-on-conflict
// and returns the updated Container. The main engine sync writes the
// status subresource on every reconcile, so this write races against it.
// If the mirror is deleted concurrently, any step may return NotFound;
// the caller treats a nil Container as "nothing left to do".
func (w *dockerWatcher) patchContainerLastAction(ctx context.Context, id string, lastAction *containersv1alpha1.ContainerLastAction) (*containersv1alpha1.Container, error) {
	key := client.ObjectKey{Name: id, Namespace: w.apiNamespace}
	var result *containersv1alpha1.Container
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest containersv1alpha1.Container
		if err := w.k8s.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		patch := client.MergeFromWithOptions(latest.DeepCopy(), client.MergeFromWithOptimisticLock{})
		latest.Status.LastAction = lastAction
		if err := w.k8s.Status().Patch(ctx, &latest, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		result = &latest
		return nil
	})
	return result, err
}

// removeActionAnnotation clears the AnnotationAction annotation only if
// its value still matches the action just processed. A concurrent writer
// may replace the annotation with a different action between dispatch
// and cleanup; that new value must survive so the next reconcile picks
// it up. Callers pass either a fresh Container from patchContainerLastAction
// (which bypasses the informer cache, not yet showing the preceding status
// write) or a cached one (which may 409 on the first Patch). On conflict,
// the retry re-reads from the cache; by then it has usually caught up.
func (w *dockerWatcher) removeActionAnnotation(ctx context.Context, latest *containersv1alpha1.Container, observed string) error {
	firstAttempt := true
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if !firstAttempt {
			if err := w.k8s.Get(ctx, client.ObjectKeyFromObject(latest), latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
		}
		firstAttempt = false
		current, present := latest.Annotations[containersv1alpha1.AnnotationAction]
		if !present || current != observed {
			return nil
		}
		patch := client.MergeFromWithOptions(latest.DeepCopy(), client.MergeFromWithOptimisticLock{})
		delete(latest.Annotations, containersv1alpha1.AnnotationAction)
		if err := w.k8s.Patch(ctx, latest, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		return nil
	})
}

// deleteContainer removes a container from Docker. NotFound is treated
// as success; other errors propagate so the caller keeps the finalizer
// and retries.
//
// Force: true expresses user intent: a K8s-side delete of a Container
// mirror means "remove this container", so Docker stops it first if
// running. Images use Force: false so in-use images are kept and the
// finalizer retries until the last consumer goes away.
func (w *dockerWatcher) deleteContainer(ctx context.Context, c *containersv1alpha1.Container) error {
	_, err := w.cli.ContainerRemove(ctx, c.Name, mobyclient.ContainerRemoveOptions{Force: true})
	if errdefs.IsNotFound(err) {
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
	_, err := w.cli.ImageRemove(ctx, ref, mobyclient.ImageRemoveOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// deleteVolume removes a volume from Docker. See deleteContainer for the
// error-handling contract.
func (w *dockerWatcher) deleteVolume(ctx context.Context, v *containersv1alpha1.Volume) error {
	_, err := w.cli.VolumeRemove(ctx, v.Status.Name, mobyclient.VolumeRemoveOptions{Force: true})
	if errdefs.IsNotFound(err) {
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

	log.Info("Full sync complete", "errors", len(errs))
	return errors.Join(errs...)
}
