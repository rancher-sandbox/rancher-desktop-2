// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mobycontainer "github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// syncAllContainers lists all Docker containers and creates/updates K8s
// resources, then removes stale ones.
func (w *dockerWatcher) syncAllContainers(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ContainerList(ctx, dockerclient.ContainerListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	// Track which container IDs exist in Docker.
	dockerIDs := make(map[string]bool, len(listResult.Items))

	var errs []error
	for _, dc := range listResult.Items {
		dockerIDs[dc.ID] = true
		if err := w.syncContainer(ctx, dc.ID); err != nil {
			errs = append(errs, err)
		}
	}

	// Remove stale K8s containers.
	var k8sContainers containersv1alpha1.ContainerList
	if err := w.k8s.List(ctx, &k8sContainers, client.InNamespace(apiNamespace)); err != nil {
		return fmt.Errorf("failed to list K8s containers: %w", err)
	}
	for i := range k8sContainers.Items {
		c := &k8sContainers.Items[i]
		if !dockerIDs[c.Name] {
			log.V(1).Info("Removing stale container", "id", c.Name)
			if err := w.removeMirrorResource(ctx, c, c.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncContainer inspects a single container and creates/updates the K8s resource.
func (w *dockerWatcher) syncContainer(ctx context.Context, id string) error {
	result, err := w.cli.ContainerInspect(ctx, id, dockerclient.ContainerInspectOptions{})
	if err != nil {
		return fmt.Errorf("failed to inspect container %s: %w", id, err)
	}

	return w.applyContainer(ctx, result.Container)
}

// applyContainer creates or updates a Container resource from a Docker
// InspectResponse. The spec is only set on creation; subsequent syncs
// update only the status subresource so user-initiated spec.state
// changes (start/stop) are not overwritten.
func (w *dockerWatcher) applyContainer(ctx context.Context, inspect mobycontainer.InspectResponse) error {
	namespace, name := parseContainerName(inspect.Name)

	// Create the resource if it doesn't exist. spec.state is always set to
	// "unknown" on creation — meaning the engine mirrors Docker state without
	// expressing intent. The user can later set it to "running" or "created"
	// to control the container; the reconciler ignores "unknown".
	var existing containersv1alpha1.Container
	err := w.k8s.Get(ctx, client.ObjectKey{Name: inspect.ID, Namespace: apiNamespace}, &existing)
	if apierrors.IsNotFound(err) {
		applyConfig := containersv1alpha1apply.Container(inspect.ID, apiNamespace).
			WithFinalizers(mirrorFinalizer).
			WithSpec(containersv1alpha1apply.ContainerSpec().WithState(containersv1alpha1.ContainerStatusUnknown))
		if err := w.k8s.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner(controllerName)); err != nil {
			return fmt.Errorf("failed to create container %s: %w", inspect.ID, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get container %s: %w", inspect.ID, err)
	}

	// Build status.
	statusApply := containersv1alpha1apply.ContainerStatus().
		WithName(name).
		WithNamespace(namespace).
		WithPath(inspect.Path).
		WithArgs(inspect.Args...).
		WithImage(inspect.Image).
		WithLabels(inspect.Config.Labels).
		WithStatus(containersv1alpha1.ContainerStatusValue(inspect.State.Status)).
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

	// Port bindings.
	var applyPorts []*containersv1alpha1apply.ContainerPortApplyConfiguration
	if inspect.NetworkSettings != nil {
		for portName, ports := range inspect.NetworkSettings.Ports {
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

