// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	apievents "github.com/containerd/containerd/api/events"
	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/core/transfer"
	transferimage "github.com/containerd/containerd/v2/core/transfer/image"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/containerd/v2/defaults"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	typeurl "github.com/containerd/typeurl/v2"
	"github.com/distribution/reference"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/api"
)

var _ engine = (*containerdWatcher)(nil)

// unexpectedStatusRegex matches error messages indicating an unexpected status
// from a registry, capturing the HTTP status code.
var unexpectedStatusRegex = regexp.MustCompile(`unexpected status from [A-Z]+ request to \S+: (\d{3})\b`)

// containerdWatcher manages a containerd client connection and event stream.
// It performs a full sync on connect and then watches for incremental changes.
type containerdWatcher struct {
	mirrorClient

	cli *containerdclient.Client

	cancel context.CancelFunc
	done   chan struct{}

	// startsMu guards starts.
	startsMu sync.Mutex
	// starts holds the start time of every task this watcher saw start, keyed
	// by mirror name. containerd reports no start time, so an observed
	// TaskStart is the only source for one. Only ContainerDelete prunes it, so
	// a delete event the watcher missed leaks an entry until it restarts.
	starts map[string]metav1.Time

	// enqueue is used to trigger reconciliation in the engine reconciler.
	enqueue func()
}

// newContainerdWatcher creates a containerd client, performs a full sync, and
// starts the event stream watcher goroutine.
func newContainerdWatcher(ctx context.Context, k8s client.Client, apiNamespace string, enqueue func()) (*containerdWatcher, error) {
	// Match images against the guest's platform, Linux on the host's
	// architecture. On Windows the host's default platform does not match a
	// Linux manifest, so Image.Spec would fail for every multi-platform image.
	guest := platforms.Only(ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH})
	cli, err := containerdclient.New(
		instance.ContainerdSocket(),
		containerdclient.WithDefaultPlatform(guest),
		containerdclient.WithDefaultNamespace(namespaces.Default),
	)
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
		mirrorClient: mirrorClient{k8s: k8s, apiNamespace: apiNamespace},
		cli:          cli,
		cancel:       watchCancel,
		done:         make(chan struct{}),
		starts:       make(map[string]metav1.Time),
		enqueue:      enqueue,
	}

	// Subscribe before the initial fullSync, the opposite order from the
	// Docker watcher. containerd's event service has no Since/replay, so the
	// subscription must already be open before the snapshot is taken;
	// otherwise events fired during fullSync are lost. Events that race the
	// snapshot are re-applied afterwards, and handleEvent is idempotent.
	eventCh, errCh := cli.Subscribe(watchCtx,
		`topic~="^/containers/"`, `topic~="^/tasks/"`, `topic~="^/images/"`,
		`topic~="^/namespaces/"`)

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

// run is the main watcher goroutine.
//
// run owns the containerd client's lifetime: it closes cli before
// returning, so a caller that observes alive()==false can drop its
// reference to the watcher without a separate cleanup step.
func (w *containerdWatcher) run(ctx context.Context, eventCh <-chan *events.Envelope, errCh <-chan error) {
	log := logf.FromContext(ctx).WithName("containerd-watcher")
	// Defers fire LIFO, giving this exit sequence:
	//
	//   1. close(w.done): alive() now returns false
	//   2. w.cli.Close(): containerd client released
	//   3. w.enqueue(): reconciler wakes and sees !alive()
	//
	// The order matters: if w.enqueue ran before w.done closed,
	// the reconciler could wake, see alive()==true on the about-to-exit
	// goroutine, and skip the restart. Closing cli between done and
	// enqueue means the reconciler observes the dead watcher only
	// after its client has been released.
	defer w.enqueue()
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
			// enqueue.
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
		mirrorName := api.MirrorName[*containersv1alpha1.Container](ev.ID, e.Namespace)
		w.forgetTaskStart(mirrorName)
		return w.removeMirrorResource(ctx, &containersv1alpha1.Container{}, mirrorName)
	case *apievents.TaskCreate:
		log.V(1).Info("Task created", "namespace", e.Namespace, "id", ev.ContainerID)
		return w.syncContainer(ctx, e.Namespace, ev.ContainerID)
	case *apievents.TaskStart:
		log.V(1).Info("Task started", "namespace", e.Namespace, "id", ev.ContainerID)
		w.recordTaskStart(api.MirrorName[*containersv1alpha1.Container](ev.ContainerID, e.Namespace), e.Timestamp)
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
	case *apievents.NamespaceCreate:
		// ev.Name is where the event type carries the subject. containerd
		// stamps the envelope namespace with the same name before publishing,
		// so e.Namespace happens to agree, but only on the namespace topics.
		log.V(1).Info("Namespace created", "namespace", ev.Name)
		return w.applyNamespace(ctx, ev.Name)
	case *apievents.NamespaceDelete:
		log.V(1).Info("Namespace deleted", "namespace", ev.Name)
		return w.removeNamespace(ctx, ev.Name)
	case *apievents.NamespaceUpdate:
		log.V(1).Info("Namespace updated", "namespace", ev.Name)
		return w.applyNamespace(ctx, ev.Name)
	default:
		return nil
	}
}

// hasTTY is not wired for containerd. nerdctl owns the container log files
// inside the VM, and containerd itself exposes no log API.
func (w *containerdWatcher) hasTTY(_ context.Context, _ *containersv1alpha1.Container) (bool, error) {
	return false, errLogsNotSupported
}

// getLogs is not wired for containerd. nerdctl owns the container log files
// inside the VM, and containerd itself exposes no log API.
func (w *containerdWatcher) getLogs(_ context.Context, _ *containersv1alpha1.Container, _ ...engineLogOptions) (io.ReadCloser, error) {
	return nil, errLogsNotSupported
}

// getSnapshotter returns the containerd snapshotter to use, for the given
// container namespace.
func (w *containerdWatcher) getSnapshotter(ctx context.Context, namespace string) (string, error) {
	if namespace == "" {
		namespace = w.cli.DefaultNamespace()
	}
	ctx = namespaces.WithNamespace(ctx, namespace)
	snapshotter, err := w.cli.GetLabel(ctx, defaults.DefaultSnapshotterNSLabel)
	if err != nil {
		return "", err
	}
	if snapshotter != "" {
		return snapshotter, nil
	}
	// Since we use a Linux VM, the fallback is "overlayfs", not the client
	// platform default.
	return "overlayfs", nil
}

// pullImage pulls an image using containerd, tracking progress and handling
// completion.
func (w *containerdWatcher) pullImage(ctx context.Context, namespace, repoTag string, onProgress func(start, current, total int64, units string), onComplete func(error)) error {
	// We need to convert repoTag to a fully qualified reference if necessary.
	imageRef, err := reference.ParseDockerRef(repoTag)
	if err != nil {
		return err
	}

	if namespace != "" {
		ctx = namespaces.WithNamespace(ctx, namespace)
	}

	snapshotter, err := w.getSnapshotter(ctx, namespace)
	if err != nil {
		return err
	}

	wg, ctx := errgroup.WithContext(ctx)
	var progressMu sync.Mutex
	// To show progress during unpack, the progress and totals are keyed by name
	// then event; on every progress updated, all other events for the same name
	// are set to max.
	progress := make(map[string]map[string]int64)
	total := make(map[string]map[string]int64)

	src, err := registry.NewOCIRegistry(ctx, imageRef.String())
	if err != nil {
		return err
	}

	progressCtx, progressCancel := context.WithCancel(ctx)

	dest := transferimage.NewStore(
		imageRef.String(),
		transferimage.WithPlatforms(ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH}),
		transferimage.WithUnpack(ocispec.Platform{OS: "linux", Architecture: runtime.GOARCH}, snapshotter),
	)

	progressFn := func(p transfer.Progress) {
		if onProgress == nil || p.Name == "" {
			return
		}
		progressMu.Lock()
		if _, ok := total[p.Name]; !ok {
			total[p.Name] = make(map[string]int64)
		}
		total[p.Name][p.Event] = p.Total
		if _, ok := progress[p.Name]; !ok {
			progress[p.Name] = make(map[string]int64)
		}
		for k := range progress[p.Name] {
			progress[p.Name][k] = total[p.Name][k]
		}
		progress[p.Name][p.Event] = p.Progress
		progressMu.Unlock()
	}

	wg.Go(func() error {
		// Pull is blocking; use a waitgroup to run it asynchronously.
		err := w.cli.Transfer(ctx, src, dest, transfer.WithProgress(progressFn))
		progressCancel()
		if err != nil {
			// Convert unexpected status errors as needed.  The error struct does not
			// survive the gRPC connection, so it's all stringly-typed.
			if match := unexpectedStatusRegex.FindStringSubmatch(err.Error()); len(match) > 0 {
				switch match[1] {
				case "401":
					return fmt.Errorf("%w: %w", cerrdefs.ErrUnauthenticated, err)
				case "403":
					return fmt.Errorf("%w: %w", cerrdefs.ErrPermissionDenied, err)
				}
			}
		}
		return err
	})
	if onProgress != nil {
		wg.Go(func() error {
			// Handle progress changes.
			ticker := time.NewTicker(500 * time.Millisecond)
			var lastProgress, lastTotal int64
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-progressCtx.Done():
					return nil // Pull complete
				case <-ticker.C:
					var allProgress int64
					var allTotal int64
					progressMu.Lock()
					for name := range total {
						for _, eventProgress := range progress[name] {
							allProgress += eventProgress
						}
						for _, eventTotal := range total[name] {
							allTotal += eventTotal
						}
					}
					progressMu.Unlock()
					if allTotal > 0 && (lastProgress != allProgress || lastTotal != allTotal) {
						onProgress(0, allProgress, allTotal, "units")
					}
					lastProgress = allProgress
					lastTotal = allTotal
				}
			}
		})
	}
	go func() {
		err := wg.Wait()
		if err == nil && onProgress != nil {
			// Emit a last progress, in case the completion events weren't triggered.
			var allTotal int64
			progressMu.Lock()
			for _, singleTotal := range total {
				for _, eventTotal := range singleTotal {
					allTotal += eventTotal
				}
			}
			progressMu.Unlock()
			if allTotal > 0 {
				onProgress(0, allTotal, allTotal, "units")
			}
		}
		if onComplete != nil {
			onComplete(err)
		}
	}()
	return nil
}

// deleteVolume returns nil, because containerd has no native volume concept.
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
	if err := w.pruneVolumes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to prune volumes: %w", err))
	}

	log.Info("Full sync complete", "errors", len(errs))
	return errors.Join(errs...)
}

// pruneVolumes deletes every Volume mirror. containerd has no volumes, so the
// engine's own state says none should exist; moby's syncVolumes reaches the
// same conclusion from a listing. Without this, a mirror left by the moby
// backend outlives a switch that skipped the stop-path sweep.
func (w *containerdWatcher) pruneVolumes(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")

	var volumeMirrors containersv1alpha1.VolumeList
	if err := w.k8s.List(ctx, &volumeMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Volumes: %w", err)
	}
	var errs []error
	for i := range volumeMirrors.Items {
		vol := &volumeMirrors.Items[i]
		log.V(1).Info("Removing stale Volume", "name", vol.Name)
		if err := w.removeMirrorResource(ctx, vol, vol.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
