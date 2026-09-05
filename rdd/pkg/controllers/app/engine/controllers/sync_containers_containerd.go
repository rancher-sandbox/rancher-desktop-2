// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// containerdMirrorName returns a deterministic RFC 1123 subdomain name for a
// containerd container. containerd IDs from nerdctl and k8s are 64-hex, so
// the common path keeps the container ID as the mirror name, matching the
// moby UX. IDs that are not valid K8s object names are hashed with a "ctr-"
// prefix.
//
// A hand-picked valid ID duplicated across two containerd namespaces maps to
// one mirror; this is an accepted caveat, as nerdctl and k8s generate
// globally unique IDs, so the collision stays theoretical.
func containerdMirrorName(ns, id string) string {
	if len(validation.IsDNS1123Subdomain(id)) == 0 {
		return id
	}
	sum := sha256.Sum256([]byte(ns + "/" + id))
	return fmt.Sprintf("ctr-%x", sum)
}

// syncAllContainers lists all containerd containers across every namespace,
// creates or updates their Container mirrors, and prunes stale ones.
func (w *containerdWatcher) syncAllContainers(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")

	nsNames, err := w.cli.NamespaceService().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	// Track which mirror names exist so stale mirrors can be pruned.
	activeNames := make(map[string]bool)

	// A namespace listing only fails when containerd itself stops answering,
	// which fails every namespace and would leave activeNames empty and prune
	// every mirror. Fail the sync instead and let the reconciler retry the
	// watcher, matching what the moby path does when ContainerList fails.
	// Per-container failures below are logged and skipped, as they are on
	// the moby path.
	var errs []error
	for _, ns := range nsNames {
		nsCtx := namespaces.WithNamespace(ctx, ns)
		ctrs, err := w.cli.Containers(nsCtx)
		if err != nil {
			return fmt.Errorf("failed to list containers in namespace %s: %w", ns, err)
		}
		for _, ctr := range ctrs {
			activeNames[containerdMirrorName(ns, ctr.ID())] = true
			if err := w.applyContainer(nsCtx, ns, ctr); err != nil {
				log.Error(err, "Skipping container during full sync", "namespace", ns, "id", ctr.ID())
			}
		}
	}

	// Drop remembered start times for containers that are gone. A delete seen
	// while the watcher was down never reached forgetTaskStart, so without
	// this the map keeps an entry for every container that ever ran.
	w.startsMu.Lock()
	maps.DeleteFunc(w.starts, func(mirrorName string, _ metav1.Time) bool {
		return !activeNames[mirrorName]
	})
	w.startsMu.Unlock()

	// Remove stale Container mirrors.
	var containerMirrors containersv1alpha1.ContainerList
	if err := w.k8s.List(ctx, &containerMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Containers: %w", err)
	}
	for i := range containerMirrors.Items {
		c := &containerMirrors.Items[i]
		if !activeNames[c.Name] {
			log.V(1).Info("Removing stale Container", "id", c.Name)
			if err := w.removeMirrorResource(ctx, c, c.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncContainer loads a single containerd container and applies the
// corresponding Container mirror. NotFound is success: the container raced a
// concurrent delete between the event and the load; the ContainerDelete
// event or the next full sync prunes any stale mirror.
func (w *containerdWatcher) syncContainer(ctx context.Context, ns, id string) error {
	nsCtx := namespaces.WithNamespace(ctx, ns)
	ctr, err := w.cli.LoadContainer(nsCtx, id)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", id, err)
	}
	return w.applyContainer(nsCtx, ns, ctr)
}

// applyContainer creates or updates a Container mirror from a containerd
// container. The mirror is status-only from the engine's side: Container has
// no desired-state spec fields, and actions are requested via the
// AnnotationAction annotation (handled separately).
func (w *containerdWatcher) applyContainer(nsCtx context.Context, ns string, ctr containerdclient.Container) error {
	info, err := ctr.Info(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get container info %s: %w", ctr.ID(), err)
	}
	mirrorName := containerdMirrorName(ns, info.ID)

	// containerd has no display-name concept; nerdctl stores it in this label.
	name := info.Labels["nerdctl/name"]
	if name == "" {
		name = info.ID
	}

	// Docker reports the image ID here, not the reference, and the UI joins
	// this against Image.status.id to find the mirror. containerd records only
	// the reference, so resolve it to the same digest the image mirror
	// reports. An image deleted out from under a running container (containerd
	// keeps none of Docker's in-use protection) no longer resolves, and the
	// reference is left in place as the more useful of the two.
	image := info.Image
	if img, err := w.cli.GetImage(nsCtx, info.Image); err != nil {
		logf.FromContext(nsCtx).WithName("containerd-watcher").
			V(1).Info("Reporting the image reference, which did not resolve",
			"id", info.ID, "image", info.Image, "error", err)
	} else {
		image = img.Target().Digest.String()
	}

	statusApply := containersv1alpha1apply.ContainerStatus().
		WithName(name).
		WithNamespace(ns).
		WithImage(image).
		WithLabels(info.Labels).
		WithCreatedAt(metav1.NewTime(info.CreatedAt))

	// The OCI spec holds the command line. Docker reports it split as Path plus
	// Args, so match that split here. A container record always carries a spec,
	// but an unreadable one only costs these two fields, so log and continue.
	if argv, err := containerdProcessArgs(info); err != nil {
		logf.FromContext(nsCtx).WithName("containerd-watcher").
			V(1).Info("Skipping path and args", "id", info.ID, "error", err)
	} else if len(argv) > 0 {
		statusApply.WithPath(argv[0]).WithArgs(argv[1:]...)
	}

	// nerdctl records a container-level failure here; containerd itself has no
	// equivalent of Docker's State.Error.
	if e := info.Labels["nerdctl/error"]; e != "" {
		statusApply.WithError(e)
	}

	// containerd knows nothing about published ports; nerdctl records the
	// mapping it asked CNI for. Containers created through CRI carry no such
	// label, so pod ports stay empty.
	ports, err := containerdPorts(info)
	if err != nil {
		logf.FromContext(nsCtx).WithName("containerd-watcher").
			V(1).Info("Skipping ports", "id", info.ID, "error", err)
	}
	statusApply.WithPorts(ports...)

	// Derive the run state from the task, if any.
	status := containersv1alpha1.ContainerStatusCreated
	task, err := ctr.Task(nsCtx, nil)
	switch {
	case errdefs.IsNotFound(err):
		// No task: nothing to report a run state from, so the mirror stays
		// Created. A container whose task was reaped lands here too.
	case err != nil:
		return fmt.Errorf("failed to load task for %s: %w", info.ID, err)
	default:
		st, err := task.Status(nsCtx)
		switch {
		case errdefs.IsNotFound(err):
			// The task raced its own deletion between load and status.
		case err != nil:
			return fmt.Errorf("failed to get task status for %s: %w", info.ID, err)
		default:
			status = mapContainerdProcessStatus(st.Status)
			pid := int32(task.Pid())
			statusApply.
				WithPid(pid).
				WithExitCode(int32(st.ExitStatus))
			if !st.ExitTime.IsZero() {
				statusApply.WithFinishedAt(metav1.NewTime(st.ExitTime))
			}
			if startedAt := w.startedAt(nsCtx, mirrorName, pid); startedAt != nil {
				statusApply.WithStartedAt(*startedAt)
			}
		}
	}
	statusApply.WithStatus(status)

	// Re-assert the mirror finalizer on every sync so a user who
	// `kubectl edit`s it away cannot bypass the engine-side containerd
	// cleanup on a later delete. Skip re-assertion once the mirror is
	// Terminating: adding a finalizer to a deleting object is rejected,
	// and processContainerFinalizers is about to strip the finalizer
	// anyway. The finalizer-only apply also creates the object: applying
	// to the status subresource cannot create a missing one.
	var existing containersv1alpha1.Container
	err = w.k8s.Get(nsCtx, client.ObjectKey{Name: mirrorName, Namespace: w.apiNamespace}, &existing)
	if apierrors.IsNotFound(err) || (err == nil && existing.DeletionTimestamp == nil) {
		finalizerOnly := containersv1alpha1apply.Container(mirrorName, w.apiNamespace).
			WithFinalizers(mirrorFinalizer)
		if err := w.k8s.Apply(nsCtx, finalizerOnly,
			client.ForceOwnership, client.FieldOwner(controllerName)); err != nil {
			return fmt.Errorf("failed to apply container finalizer %s: %w", mirrorName, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get container %s: %w", mirrorName, err)
	}

	err = w.k8s.Status().Apply(nsCtx,
		containersv1alpha1apply.Container(mirrorName, w.apiNamespace).
			WithStatus(statusApply),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply container status %s: %w", mirrorName, err)
	}

	return nil
}

// mapContainerdProcessStatus maps a containerd process status to the CRD
// enum. Unrecognised values fall through to ContainerStatusUnknown so a new
// containerd status string does not fail SSA validation and silently drop
// the mirror update.
func mapContainerdProcessStatus(s containerdclient.ProcessStatus) containersv1alpha1.ContainerStatusValue {
	switch s {
	case containerdclient.Created:
		return containersv1alpha1.ContainerStatusCreated
	case containerdclient.Running:
		return containersv1alpha1.ContainerStatusRunning
	case containerdclient.Stopped:
		return containersv1alpha1.ContainerStatusExited
	case containerdclient.Paused:
		return containersv1alpha1.ContainerStatusPaused
	case containerdclient.Pausing:
		return containersv1alpha1.ContainerStatusPausing
	}
	return containersv1alpha1.ContainerStatusUnknown
}

// containerdProcessArgs returns the container's full command line from its OCI
// runtime spec. The spec is unmarshalled from the record already fetched, so
// this costs no extra containerd round trip.
func containerdProcessArgs(info containers.Container) ([]string, error) {
	if info.Spec == nil {
		return nil, errors.New("container record has no runtime spec")
	}
	var spec oci.Spec
	if err := json.Unmarshal(info.Spec.GetValue(), &spec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal runtime spec: %w", err)
	}
	if spec.Process == nil {
		return nil, errors.New("runtime spec has no process")
	}
	return spec.Process.Args, nil
}

// recordTaskStart remembers when a task was seen starting. containerd reports
// no start time of its own, so an observed TaskStart event is the only place
// one can come from.
func (w *containerdWatcher) recordTaskStart(mirrorName string, at time.Time) {
	w.startsMu.Lock()
	defer w.startsMu.Unlock()
	w.starts[mirrorName] = metav1.NewTime(at)
}

// forgetTaskStart drops a remembered start time, so the map does not grow with
// every container that has ever run.
func (w *containerdWatcher) forgetTaskStart(mirrorName string) {
	w.startsMu.Lock()
	defer w.startsMu.Unlock()
	delete(w.starts, mirrorName)
}

// startedAt returns the start time to report for a running task. A start this
// watcher saw wins. Otherwise the value already on the mirror is reused, but
// only while the PID matches: a different PID is a task recreated out of
// sight, whose old start time no longer describes it. Both miss for a task
// already running when the watcher connected, and the field stays unset
// rather than reporting a time containerd cannot support.
func (w *containerdWatcher) startedAt(ctx context.Context, mirrorName string, pid int32) *metav1.Time {
	w.startsMu.Lock()
	at, ok := w.starts[mirrorName]
	w.startsMu.Unlock()
	if ok {
		return &at
	}

	// Reads come from the informer cache, so this costs no API round trip.
	var existing containersv1alpha1.Container
	key := client.ObjectKey{Name: mirrorName, Namespace: w.apiNamespace}
	if err := w.k8s.Get(ctx, key, &existing); err != nil {
		return nil
	}
	if existing.Status.StartedAt.IsZero() || existing.Status.Pid != pid {
		return nil
	}
	return &existing.Status.StartedAt
}

// cniPortMapping is the shape nerdctl marshals into the nerdctl/ports label,
// matching github.com/containerd/go-cni's PortMapping. It is declared here
// rather than imported so the mirror does not take a dependency on go-cni for
// four fields; the names are part of nerdctl's on-disk format.
type cniPortMapping struct {
	HostPort      int32
	ContainerPort int32
	Protocol      string
	HostIP        string
}

// containerdPorts builds the port mirrors from nerdctl's label. Entries are
// grouped by the "port/protocol" name Docker uses, and both the names and the
// bindings within each name are sorted: the fields are atomic under SSA, so an
// unstable order would mint a new resourceVersion on every sync.
func containerdPorts(info containers.Container) ([]*containersv1alpha1apply.ContainerPortApplyConfiguration, error) {
	raw := info.Labels["nerdctl/ports"]
	if raw == "" {
		return nil, nil
	}
	var mappings []cniPortMapping
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		return nil, fmt.Errorf("failed to unmarshal nerdctl/ports: %w", err)
	}

	byName := make(map[string][]cniPortMapping, len(mappings))
	for _, m := range mappings {
		name := fmt.Sprintf("%d/%s", m.ContainerPort, m.Protocol)
		byName[name] = append(byName[name], m)
	}

	ports := make([]*containersv1alpha1apply.ContainerPortApplyConfiguration, 0, len(byName))
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		bound := byName[name]
		slices.SortFunc(bound, func(a, b cniPortMapping) int {
			if a.HostIP != b.HostIP {
				return strings.Compare(a.HostIP, b.HostIP)
			}
			return cmp.Compare(a.HostPort, b.HostPort)
		})
		bindings := make([]*containersv1alpha1apply.ContainerPortBindingApplyConfiguration, 0, len(bound))
		for _, m := range bound {
			bindings = append(bindings, containersv1alpha1apply.ContainerPortBinding().
				WithHostIP(m.HostIP).
				WithHostPort(strconv.Itoa(int(m.HostPort))))
		}
		ports = append(ports, containersv1alpha1apply.ContainerPort().
			WithName(name).
			WithBindings(bindings...))
	}
	return ports, nil
}
