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
	"time"

	apievents "github.com/containerd/containerd/api/events"
	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	typeurl "github.com/containerd/typeurl/v2"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

var _ engine = (*containerdWatcher)(nil)

// containerdWatcher manages a containerd client connection and event stream.
// It performs a full sync on connect and then watches for incremental changes.
type containerdWatcher struct {
	mirrorClient

	cli *containerdclient.Client

	cancel context.CancelFunc
	done   chan struct{}

	// reconcileChan is used to trigger reconciliation in the engine reconciler.
	reconcileChan chan<- event.GenericEvent
}

// newContainerdWatcher creates a containerd client, performs a full sync, and
// starts the event stream watcher goroutine.
func newContainerdWatcher(ctx context.Context, k8s client.Client, apiNamespace string, reconcileChan chan<- event.GenericEvent) (*containerdWatcher, error) {
	cli, err := containerdclient.New(instance.ContainerdSocket())
	if err != nil {
		return nil, fmt.Errorf("failed to create containerd client: %w", err)
	}

	// Verify the connection.
	servingCtx, servingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer servingCancel()
	serving, err := cli.IsServing(servingCtx)
	if err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to reach containerd: %w", err)
	}
	if !serving {
		cli.Close()
		return nil, errors.New("containerd is not serving")
	}

	watchCtx, watchCancel := context.WithCancel(ctx)

	w := &containerdWatcher{
		mirrorClient:  mirrorClient{k8s: k8s, apiNamespace: apiNamespace},
		cli:           cli,
		cancel:        watchCancel,
		done:          make(chan struct{}),
		reconcileChan: reconcileChan,
	}

	// Subscribe before the initial fullSync — the opposite order from the
	// Docker watcher. containerd's event service has no Since/replay, so the
	// subscription must already be open before the snapshot is taken;
	// otherwise events fired during fullSync are lost. Events that race the
	// snapshot are re-applied afterwards, and handleEvent is idempotent.
	eventCh, errCh := cli.Subscribe(watchCtx, `topic~="^/containers/"`, `topic~="^/tasks/"`, `topic~="^/images/"`)

	if err := w.fullSync(watchCtx); err != nil {
		watchCancel()
		cli.Close()
		return nil, fmt.Errorf("failed to perform initial sync: %w", err)
	}

	go w.run(watchCtx, eventCh, errCh)

	return w, nil
}

// stop cancels the watcher goroutine and waits for it to finish.
// run's deferred cleanup closes the containerd client; stop only signals
// the goroutine and blocks until it exits.
func (w *containerdWatcher) stop() {
	w.cancel()
	<-w.done
}

// alive returns true if the watcher goroutine is still running.
func (w *containerdWatcher) alive() bool {
	select {
	case <-w.done:
		return false
	default:
		return true
	}
}

// enqueueReconcile wakes the engine reconciler. The channel has a
// buffer of one, so enqueueReconcile is a no-op when a reconcile is
// already queued — the reconciler will pick up the current watcher
// state when it runs.
func (w *containerdWatcher) enqueueReconcile() {
	select {
	case w.reconcileChan <- event.GenericEvent{}:
	default:
	}
}

// run is the main watcher goroutine.
//
// run owns the containerd client's lifetime: it closes cli before
// returning, so a caller that observes alive()==false can drop its
// reference to the watcher without a separate cleanup step.
func (w *containerdWatcher) run(ctx context.Context, eventCh <-chan *events.Envelope, errCh <-chan error) {
	log := logf.FromContext(ctx).WithName("containerd-watcher")
	// Defers fire LIFO, giving this exit sequence:
	//
	//   1. close(w.done)       — alive() now returns false
	//   2. w.cli.Close()       — containerd client released
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
			log.Error(nil, "panic in containerd watcher goroutine",
				"recovered", r, "stack", string(debug.Stack()))
		}
	}()

	for {
		select {
		case <-ctx.Done():
			log.Info("Containerd watcher stopping")
			return
		case err := <-errCh:
			if ctx.Err() != nil {
				return
			}
			// The reconciler restarts the watcher via the deferred
			// enqueueReconcile.
			log.Error(err, "Containerd event stream error")
			return
		case e, ok := <-eventCh:
			if !ok {
				log.Info("Containerd event stream closed")
				return
			}
			// Transient handleEvent errors (API error, SSA conflict past
			// its internal retry) are logged and dropped. Container events
			// self-heal on the next state change; a dropped apply leaves the
			// mirror stale until the next full resync.
			if err := w.handleEvent(ctx, e); err != nil {
				log.Error(err, "Failed to handle containerd event",
					"topic", e.Topic, "namespace", e.Namespace)
			}
		}
	}
}

// handleEvent processes a single containerd event.
func (w *containerdWatcher) handleEvent(ctx context.Context, e *events.Envelope) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")

	decoded, err := typeurl.UnmarshalAny(e.Event)
	if err != nil {
		return fmt.Errorf("failed to unmarshal containerd event: %w", err)
	}

	switch ev := decoded.(type) {
	case *apievents.ContainerCreate:
		log.V(1).Info("Container created", "namespace", e.Namespace, "id", ev.ID)
		// Namespaces appear implicitly on first use; fullSync only catches
		// pre-existing ones, so apply the namespace mirror before the
		// container.
		if err := w.applyNamespace(ctx, e.Namespace); err != nil {
			return err
		}
		return w.syncContainer(ctx, e.Namespace, ev.ID)
	case *apievents.ContainerUpdate:
		// Labels such as nerdctl/name can change on update.
		log.V(1).Info("Container updated", "namespace", e.Namespace, "id", ev.ID)
		return w.syncContainer(ctx, e.Namespace, ev.ID)
	case *apievents.ContainerDelete:
		log.V(1).Info("Container deleted", "namespace", e.Namespace, "id", ev.ID)
		return w.removeMirrorResource(ctx, &containersv1alpha1.Container{},
			containerdMirrorName(e.Namespace, ev.ID))
	case *apievents.TaskCreate:
		log.V(1).Info("Task created", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskStart:
		log.V(1).Info("Task started", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskExit:
		log.V(1).Info("Task exited", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskDelete:
		log.V(1).Info("Task deleted", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskPaused:
		log.V(1).Info("Task paused", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskResumed:
		log.V(1).Info("Task resumed", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskOOM:
		log.V(1).Info("Task OOM", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.ImageCreate:
		log.V(1).Info("Image created", "namespace", e.Namespace, "name", ev.Name)
		// Namespaces appear implicitly on first use; fullSync only catches
		// pre-existing ones, so apply the namespace mirror before the image.
		if err := w.applyNamespace(ctx, e.Namespace); err != nil {
			return err
		}
		return w.syncImage(ctx, e.Namespace, ev.Name)
	case *apievents.ImageUpdate:
		log.V(1).Info("Image updated", "namespace", e.Namespace, "name", ev.Name)
		return w.syncImage(ctx, e.Namespace, ev.Name)
	case *apievents.ImageDelete:
		log.V(1).Info("Image deleted", "namespace", e.Namespace, "name", ev.Name)
		return w.removeMirrorResource(ctx, &containersv1alpha1.Image{},
			containerdImageMirrorName(e.Namespace, ev.Name))
	default:
		return nil
	}
}

// hasTTY is not wired for containerd: nerdctl owns the container log files
// inside the VM, and containerd itself exposes no log API.
func (w *containerdWatcher) hasTTY(_ context.Context, _ *containersv1alpha1.Container) (bool, error) {
	return false, errors.New("container logs are not supported with the containerd engine yet")
}

// getLogs is not wired for containerd: nerdctl owns the container log files
// inside the VM, and containerd itself exposes no log API.
func (w *containerdWatcher) getLogs(_ context.Context, _ *containersv1alpha1.Container, _ ...engineLogOptions) (io.ReadCloser, error) {
	return nil, errors.New("container logs are not supported with the containerd engine yet")
}

// deleteVolume returns nil: containerd has no native volume concept.
func (w *containerdWatcher) deleteVolume(_ context.Context, _ *containersv1alpha1.Volume) error {
	return nil
}

// fullSync lists namespaces, containers, and images from containerd and
// creates corresponding mirror resources, pruning stale ones. containerd has
// no volumes.
func (w *containerdWatcher) fullSync(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")
	log.Info("Starting full sync")

	var errs []error

	if err := w.syncNamespaces(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync namespaces: %w", err))
	}
	if err := w.syncAllContainers(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync containers: %w", err))
	}
	if err := w.syncAllImages(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to sync images: %w", err))
	}

	log.Info("Full sync complete", "errors", len(errs))
	return errors.Join(errs...)
}
