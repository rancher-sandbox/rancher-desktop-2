// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

// syncAllContainers lists all Docker containers and creates/updates
// `Container` mirror resources, then removes stale ones.
//
// Containers are Inspected sequentially rather than in parallel. The
// initial sync runs one-shot at watcher startup, typical dev machines
// have tens of containers (not hundreds), and the status fields we
// surface (port bindings, labels, exit code) require Inspect — the
// List response does not expose them. If startup latency becomes a
// concern on loaded machines, parallelise with errgroup + a small
// worker pool here rather than redesigning the sync.
func (w *dockerWatcher) syncAllContainers(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ContainerList(ctx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Track which Docker container IDs exist, so stale mirrors can be pruned.
	dockerIDs := make(map[string]bool, len(listResult.Items))

	// Per-item inspect failures are logged and skipped rather than
	// failing the whole startup: a single permanently-broken Inspect
	// (engine-side corruption, transient race) must not prevent every
	// other healthy container from being mirrored, nor pin the
	// ContainerEngineReady condition at ConnectFailed. Structural
	// errors (k8s list, stale-mirror cleanup) are still fatal.
	var errs []error
	for _, dc := range listResult.Items {
		dockerIDs[dc.ID] = true
		if err := w.syncContainer(ctx, dc.ID); err != nil {
			log.Error(err, "Skipping container during full sync", "id", dc.ID)
		}
	}

	// Remove stale Container mirrors.
	var containerMirrors containersv1alpha1.ContainerList
	if err := w.k8s.List(ctx, &containerMirrors, client.InNamespace(apiNamespace)); err != nil {
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

// syncContainer inspects a single Docker container and creates or
// updates the corresponding `Container` mirror. NotFound is treated as
// success: the container raced a concurrent delete between List and
// Inspect, and the stale Container mirror will be pruned by
// syncAllContainers' remove-stale step later in the same sync.
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

// applyContainer creates or updates a `Container` mirror from a Docker
// InspectResponse. The spec is only set on creation; subsequent syncs
// update only the status subresource so user-initiated spec.state
// changes (start/stop) are not overwritten.
//
// Pure server-side Apply is not enough here: if we always applied
// WithSpec(unknown), the engine-controller field owner would keep
// reasserting spec.state=unknown, clobbering any user patch to
// running/created. Gating the WithSpec call behind a Get — so spec is
// written exactly once, on creation — keeps the invariant "subsequent
// syncs never touch spec" without needing a second field owner.
func (w *dockerWatcher) applyContainer(ctx context.Context, inspect mobycontainer.InspectResponse) error {
	namespace, name := parseContainerName(inspect.Name)

	// Create the `Container` mirror if it doesn't exist. spec.state is
	// always set to "unknown" on creation — meaning the engine mirrors
	// Docker container state without expressing intent. The user can later
	// set it to "running" or "created" to control the Docker container;
	// the reconciler ignores "unknown".
	//
	// Deliberately omit ForceOwnership on the create apply: if a user
	// patch to spec.state landed in the window between the Get above
	// and this Apply, ForceOwnership would silently clobber it. Without
	// the flag, the Apply succeeds on a fresh resource (no other owner
	// for spec.state yet) and fails loudly on a concurrent conflict
	// (the Get-NotFound observation was racy).
	var existing containersv1alpha1.Container
	err := w.k8s.Get(ctx, client.ObjectKey{Name: inspect.ID, Namespace: apiNamespace}, &existing)
	if apierrors.IsNotFound(err) {
		applyConfig := containersv1alpha1apply.Container(inspect.ID, apiNamespace).
			WithFinalizers(mirrorFinalizer).
			WithSpec(containersv1alpha1apply.ContainerSpec().WithState(containersv1alpha1.ContainerStatusUnknown))
		if err := w.k8s.Apply(ctx, applyConfig, client.FieldOwner(controllerName)); err != nil {
			return fmt.Errorf("failed to create container %s: %w", inspect.ID, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get container %s: %w", inspect.ID, err)
	} else if existing.DeletionTimestamp == nil {
		// Re-assert the mirror finalizer on every sync of an existing
		// Container so a user that `kubectl edit`s it away cannot
		// bypass the engine-side Docker cleanup on a subsequent delete.
		// Skip the re-assertion once the mirror is Terminating: adding
		// a finalizer to a deleting object is rejected by the API
		// server, and processContainerFinalizers is about to strip the
		// finalizer anyway.
		//
		// The apply uses a dedicated finalizerFieldOwner rather than
		// controllerName. SSA treats fields absent from an apply config
		// as released by that manager; releasing spec from controllerName
		// would prune spec (no other manager owns it) and fail the
		// required-field validation on the Container CRD. A separate
		// manager that only ever touches finalizers keeps controllerName's
		// spec ownership untouched.
		finalizerOnly := containersv1alpha1apply.Container(inspect.ID, apiNamespace).
			WithFinalizers(mirrorFinalizer)
		if err := w.k8s.Apply(ctx, finalizerOnly,
			client.ForceOwnership, client.FieldOwner(finalizerFieldOwner)); err != nil {
			return fmt.Errorf("failed to reassert container finalizer %s: %w", inspect.ID, err)
		}
	}

	// Build status.
	statusApply := containersv1alpha1apply.ContainerStatus().
		WithName(name).
		WithNamespace(namespace).
		WithPath(inspect.Path).
		WithArgs(inspect.Args...).
		WithImage(inspect.Image).
		WithLabels(inspect.Config.Labels).
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

	// Port bindings. Go map iteration is randomized, so sort by port
	// name before building the apply slice: even with
	// +listType=map +listMapKey=name on Status.Ports, the entries land
	// in the stored object in the order we provide, and a stable order
	// avoids spurious resourceVersion churn when consumers render the
	// list or diff it.
	var applyPorts []*containersv1alpha1apply.ContainerPortApplyConfiguration
	if inspect.NetworkSettings != nil {
		portNames := make([]mobynetwork.Port, 0, len(inspect.NetworkSettings.Ports))
		for p := range inspect.NetworkSettings.Ports {
			portNames = append(portNames, p)
		}
		sort.Slice(portNames, func(i, j int) bool {
			return portNames[i].String() < portNames[j].String()
		})
		for _, portName := range portNames {
			ports := inspect.NetworkSettings.Ports[portName]
			var bindings []*containersv1alpha1apply.ContainerPortBindingApplyConfiguration
			for _, port := range ports {
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

	statusConfig := containersv1alpha1apply.Container(inspect.ID, apiNamespace).
		WithStatus(statusApply)

	err = w.k8s.SubResource("status").Apply(ctx, statusConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply container status %s: %w", inspect.ID, err)
	}

	return nil
}

// parseContainerName splits a Docker container name into namespace and name.
// Docker container names start with "/" and may contain a namespace prefix
// like "/namespace/name".
func parseContainerName(fullName string) (namespace, name string) {
	fullName = strings.TrimPrefix(fullName, "/")
	namespace, name, found := strings.Cut(fullName, "/")
	if !found {
		return containerNamespace, namespace
	}
	return namespace, name
}

// mapDockerContainerState maps a free-form Docker State.Status string to
// the CRD enum. Unknown values are mapped to ContainerStatusUnknown so a
// new Docker state (hypothetical "initializing", "error", etc.) does not
// fail SSA validation and silently drop the mirror update — the Docker
// API contract permits adding new state strings in minor releases.
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
