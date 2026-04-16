// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobycontainer "github.com/moby/moby/api/types/container"
	mobynetwork "github.com/moby/moby/api/types/network"
	dockerclient "github.com/moby/moby/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// syncAllContainers lists all Docker containers, creates or updates
// their Container mirrors, and prunes stale ones.
//
// Inspect runs sequentially: the sync is one-shot at watcher startup,
// typical dev machines have tens of containers, and the fields we
// surface (ports, labels, exit code) are only on Inspect, not List.
// Parallelise with errgroup here if startup latency on loaded machines
// becomes a concern.
func (w *dockerWatcher) syncAllContainers(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ContainerList(ctx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Track which Docker container IDs exist so stale mirrors can be pruned.
	dockerIDs := make(map[string]bool, len(listResult.Items))

	// Log and skip per-item Inspect failures rather than failing the
	// whole startup: a single permanently-broken Inspect must not
	// prevent every other container from being mirrored, nor pin
	// ContainerEngineReady at ConnectFailed. Structural errors below
	// (K8s list, stale-mirror cleanup) are still fatal.
	var errs []error
	for _, dc := range listResult.Items {
		dockerIDs[dc.ID] = true
		if err := w.syncContainer(ctx, dc.ID); err != nil {
			log.Error(err, "Skipping container during full sync", "id", dc.ID)
		}
	}

	// Remove stale Container mirrors.
	var containerMirrors containersv1alpha1.ContainerList
	if err := w.k8s.List(ctx, &containerMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Containers: %w", err)
	}
	for i := range containerMirrors.Items {
		c := &containerMirrors.Items[i]
		if !dockerIDs[c.Name] {
			log.V(1).Info("Removing stale Container", "id", c.Name)
			if err := w.removeMirrorResource(ctx, c, c.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncContainer inspects a single Docker container and applies the
// corresponding Container mirror. NotFound is success: the container
// raced a concurrent delete between List and Inspect, and the stale
// mirror is pruned later in syncAllContainers.
func (w *dockerWatcher) syncContainer(ctx context.Context, id string) error {
	result, err := w.cli.ContainerInspect(ctx, id, dockerclient.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", id, err)
	}

	return w.applyContainer(ctx, result.Container)
}

// applyContainer creates or updates a Container mirror from a Docker
// InspectResponse. Spec is written exactly once, on creation;
// subsequent syncs touch only the status subresource so user-initiated
// spec.state changes are preserved.
//
// Pure SSA is not enough: applying WithSpec(unknown) on every sync
// would make the engine-controller field owner keep reasserting
// spec.state=unknown and clobber any user patch to running/created.
// Gating the WithSpec call on a Get-NotFound keeps the "subsequent
// syncs never touch spec" invariant without a second field owner.
func (w *dockerWatcher) applyContainer(ctx context.Context, inspect mobycontainer.InspectResponse) error {
	namespace, name := parseContainerName(inspect.Name)

	// Create the Container mirror with spec.state=unknown: the engine
	// mirrors Docker state without expressing intent. The user sets
	// state to "running" or "created" to control the container;
	// "unknown" is a no-op for the reconciler.
	//
	// Omit ForceOwnership on the create apply: if a user patch to
	// spec.state landed between the Get above and this Apply,
	// ForceOwnership would silently clobber it. Without the flag, the
	// Apply succeeds on a fresh resource and fails loudly on a
	// concurrent conflict.
	var existing containersv1alpha1.Container
	err := w.k8s.Get(ctx, client.ObjectKey{Name: inspect.ID, Namespace: w.apiNamespace}, &existing)
	if apierrors.IsNotFound(err) {
		applyConfig := containersv1alpha1apply.Container(inspect.ID, w.apiNamespace).
			WithFinalizers(mirrorFinalizer).
			WithSpec(containersv1alpha1apply.ContainerSpec().WithState(containersv1alpha1.ContainerStatusUnknown))
		if err := w.k8s.Apply(ctx, applyConfig, client.FieldOwner(controllerName)); err != nil {
			return fmt.Errorf("failed to create container %s: %w", inspect.ID, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get container %s: %w", inspect.ID, err)
	} else if existing.DeletionTimestamp == nil {
		// Re-assert the mirror finalizer on every sync so a user who
		// `kubectl edit`s it away cannot bypass the engine-side Docker
		// cleanup on a later delete. Skip re-assertion once the mirror
		// is Terminating: adding a finalizer to a deleting object is
		// rejected, and processContainerFinalizers is about to strip
		// the finalizer anyway.
		//
		// Use finalizerFieldOwner, not controllerName. SSA treats
		// fields absent from an apply config as released by that
		// manager; releasing spec from controllerName would prune
		// spec (no other manager owns it) and fail the CRD's
		// required-field validation.
		finalizerOnly := containersv1alpha1apply.Container(inspect.ID, w.apiNamespace).
			WithFinalizers(mirrorFinalizer)
		if err := w.k8s.Apply(ctx, finalizerOnly,
			client.ForceOwnership, client.FieldOwner(finalizerFieldOwner)); err != nil {
			return fmt.Errorf("failed to reassert container finalizer %s: %w", inspect.ID, err)
		}
	}

	// Config is typed as a pointer and may be nil for a malformed
	// inspect response; NetworkSettings a few lines down is guarded
	// the same way.
	var labels map[string]string
	if inspect.Config != nil {
		labels = inspect.Config.Labels
	}

	// Build status.
	statusApply := containersv1alpha1apply.ContainerStatus().
		WithName(name).
		WithNamespace(namespace).
		WithPath(inspect.Path).
		WithArgs(inspect.Args...).
		WithImage(inspect.Image).
		WithLabels(labels).
		WithStatus(mapDockerContainerState(string(inspect.State.Status))).
		WithPid(int32(inspect.State.Pid)).
		WithExitCode(int32(inspect.State.ExitCode))

	if inspect.State.Error != "" {
		statusApply.WithError(inspect.State.Error)
	}

	if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}
	if t, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil && !t.IsZero() {
		statusApply.WithStartedAt(metav1.NewTime(t))
	}
	if t, err := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt); err == nil && !t.IsZero() {
		statusApply.WithFinishedAt(metav1.NewTime(t))
	}

	// Sort port bindings by name before applying. Go map iteration is
	// randomized, and even with +listType=map the entries land in the
	// stored object in the order we send them — a stable order avoids
	// spurious resourceVersion churn on every sync.
	var applyPorts []*containersv1alpha1apply.ContainerPortApplyConfiguration
	if inspect.NetworkSettings != nil {
		portNames := slices.SortedFunc(maps.Keys(inspect.NetworkSettings.Ports),
			func(p1, p2 mobynetwork.Port) int {
				return strings.Compare(p1.String(), p2.String())
			})
		for _, portName := range portNames {
			// Sort bindings by (HostIP, HostPort) for the same reason
			// portNames is sorted: ContainerPort.Bindings is atomic
			// under SSA, and Docker returns dual-stack bindings in
			// either order.
			sortedPorts := slices.SortedFunc(slices.Values(inspect.NetworkSettings.Ports[portName]),
				func(pb1, pb2 mobynetwork.PortBinding) int {
					if pb1.HostIP.String() != pb2.HostIP.String() {
						return strings.Compare(pb1.HostIP.String(), pb2.HostIP.String())
					}
					return strings.Compare(pb1.HostPort, pb2.HostPort)
				})
			var bindings []*containersv1alpha1apply.ContainerPortBindingApplyConfiguration
			for _, port := range sortedPorts {
				bindings = append(bindings, containersv1alpha1apply.ContainerPortBinding().
					WithHostIP(port.HostIP.String()).
					WithHostPort(port.HostPort))
			}
			applyPorts = append(applyPorts, containersv1alpha1apply.ContainerPort().
				WithName(portName.String()).
				WithBindings(bindings...))
		}
	}
	statusApply.WithPorts(applyPorts...)

	statusConfig := containersv1alpha1apply.Container(inspect.ID, w.apiNamespace).
		WithStatus(statusApply)

	err = w.k8s.Status().Apply(ctx, statusConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply container status %s: %w", inspect.ID, err)
	}

	return nil
}

// parseContainerName splits a Docker container name into namespace
// and name. Docker names start with "/" and may carry a namespace
// prefix like "/namespace/name".
func parseContainerName(fullName string) (namespace, name string) {
	fullName = strings.TrimPrefix(fullName, "/")
	namespace, name, found := strings.Cut(fullName, "/")
	if !found {
		return containerNamespace, namespace
	}
	return namespace, name
}

// mapDockerContainerState maps a free-form Docker State.Status string
// to the CRD enum. Unrecognised values fall through to
// ContainerStatusUnknown so a new Docker state string (added in a
// minor Docker release) does not fail SSA validation and silently
// drop the mirror update.
func mapDockerContainerState(s string) containersv1alpha1.ContainerStatusValue {
	switch v := containersv1alpha1.ContainerStatusValue(s); v {
	case containersv1alpha1.ContainerStatusCreated,
		containersv1alpha1.ContainerStatusRunning,
		containersv1alpha1.ContainerStatusPausing,
		containersv1alpha1.ContainerStatusPaused,
		containersv1alpha1.ContainerStatusRestarting,
		containersv1alpha1.ContainerStatusRemoving,
		containersv1alpha1.ContainerStatusExited,
		containersv1alpha1.ContainerStatusDead:
		return v
	}
	return containersv1alpha1.ContainerStatusUnknown
}
