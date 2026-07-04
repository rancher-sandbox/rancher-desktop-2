// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"

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

	// Track which mirror names we create so stale mirrors can be pruned.
	activeNames := make(map[string]bool)

	// Log and skip per-item failures rather than failing the whole startup:
	// a single permanently-broken namespace or container must not pin
	// ContainerEngineReady at ConnectFailed. Structural errors below are
	// still fatal.
	var errs []error
	for _, ns := range nsNames {
		nsCtx := namespaces.WithNamespace(ctx, ns)
		ctrs, err := w.cli.Containers(nsCtx)
		if err != nil {
			log.Error(err, "Skipping namespace during full sync", "namespace", ns)
			continue
		}
		for _, ctr := range ctrs {
			activeNames[containerdMirrorName(ns, ctr.ID())] = true
			if err := w.applyContainer(nsCtx, ns, ctr); err != nil {
				log.Error(err, "Skipping container during full sync", "namespace", ns, "id", ctr.ID())
			}
		}
	}

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

	statusApply := containersv1alpha1apply.ContainerStatus().
		WithName(name).
		WithNamespace(ns).
		WithImage(info.Image).
		WithLabels(info.Labels).
		WithCreatedAt(metav1.NewTime(info.CreatedAt))

	// Derive the run state from the task, if any.
	status := containersv1alpha1.ContainerStatusCreated
	task, err := ctr.Task(nsCtx, nil)
	switch {
	case errdefs.IsNotFound(err):
		// No task: the container exists but has never run.
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
			statusApply.
				WithPid(int32(task.Pid())).
				WithExitCode(int32(st.ExitStatus))
			if !st.ExitTime.IsZero() {
				statusApply.WithFinishedAt(metav1.NewTime(st.ExitTime))
			}
		}
	}
	statusApply.WithStatus(status)

	// Create the mirror before the status apply: applying to the status
	// subresource cannot create a missing object. Unlike the moby mirror,
	// no mirror finalizer is added — containerd-side deletes land in a
	// later PR, and with a finalizer but no delete handler, user deletes
	// would trap in Terminating.
	err = w.k8s.Apply(nsCtx, containersv1alpha1apply.Container(mirrorName, w.apiNamespace),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply container %s: %w", mirrorName, err)
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
