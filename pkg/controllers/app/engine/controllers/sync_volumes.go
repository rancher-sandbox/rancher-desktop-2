// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobyvolume "github.com/moby/moby/api/types/volume"
	dockerclient "github.com/moby/moby/client"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// volumeK8sName computes a deterministic RFC 1123 subdomain name for a
// Docker volume. Docker permits characters that are invalid in K8s
// object names (uppercase letters, underscores), so the Docker name is
// hashed and prefixed with "vol-" for readability. The original Docker
// name is preserved in status.name.
func volumeK8sName(dockerName string) string {
	sum := sha256.Sum256([]byte(dockerName))
	return fmt.Sprintf("vol-%x", sum)
}

// ensureNamespace creates the K8s namespace for mirror resources if it doesn't exist.
func (w *dockerWatcher) ensureNamespace(ctx context.Context) error {
	var ns corev1.Namespace
	if err := w.k8s.Get(ctx, client.ObjectKey{Name: apiNamespace}, &ns); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: apiNamespace},
		}
		if err := w.k8s.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create namespace %s: %w", apiNamespace, err)
		}
	}
	return nil
}

// syncContainerNamespace creates the "moby" ContainerNamespace resource.
// Unlike Container / Image / Volume mirrors, this resource has no mirror
// finalizer: Docker has no corresponding engine object to delete on the
// reverse path, and cleanupMirrorResources sweeps it unconditionally on
// VM stop, so a finalizer with no handler would only trap user-initiated
// deletes in Terminating until the next bounce.
func (w *dockerWatcher) syncContainerNamespace(ctx context.Context) error {
	applyConfig := containersv1alpha1apply.ContainerNamespace(containerNamespace, apiNamespace)

	return w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
}

// syncAllVolumes lists all Docker volumes and creates/updates K8s resources,
// then removes stale ones.
func (w *dockerWatcher) syncAllVolumes(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	volumeList, err := w.cli.VolumeList(ctx, dockerclient.VolumeListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	// Track which K8s resource names we create.
	activeNames := make(map[string]bool, len(volumeList.Items))

	var errs []error
	for _, v := range volumeList.Items {
		k8sName := volumeK8sName(v.Name)
		activeNames[k8sName] = true
		if err := w.applyVolume(ctx, v); err != nil {
			errs = append(errs, err)
		}
	}

	// Remove stale K8s volumes.
	var k8sVolumes containersv1alpha1.VolumeList
	if err := w.k8s.List(ctx, &k8sVolumes, client.InNamespace(apiNamespace)); err != nil {
		return fmt.Errorf("failed to list K8s volumes: %w", err)
	}
	for i := range k8sVolumes.Items {
		vol := &k8sVolumes.Items[i]
		if !activeNames[vol.Name] {
			log.V(1).Info("Removing stale volume", "name", vol.Name)
			if err := w.removeMirrorResource(ctx, vol, vol.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncVolume looks up a single volume by name and creates/updates the K8s resource.
// NotFound is treated as success: the volume raced a concurrent delete
// between List and Inspect, and syncAllVolumes' remove-stale step
// prunes the K8s mirror later in the same sync.
func (w *dockerWatcher) syncVolume(ctx context.Context, name string) error {
	result, err := w.cli.VolumeInspect(ctx, name, dockerclient.VolumeInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect volume %s: %w", name, err)
	}
	return w.applyVolume(ctx, result.Volume)
}

// applyVolume creates or updates a Volume resource from a Docker volume.
func (w *dockerWatcher) applyVolume(ctx context.Context, vol mobyvolume.Volume) error {
	k8sName := volumeK8sName(vol.Name)

	applyConfig := containersv1alpha1apply.Volume(k8sName, apiNamespace).
		WithFinalizers(mirrorFinalizer)

	err := w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume %s: %w", k8sName, err)
	}

	statusApply := containersv1alpha1apply.VolumeStatus().
		WithName(vol.Name).
		WithNamespace(containerNamespace).
		WithDriver(vol.Driver).
		WithLabels(vol.Labels).
		WithOptions(vol.Options).
		WithMountPoint(vol.Mountpoint).
		WithScope(vol.Scope)

	if t, err := time.Parse(time.RFC3339Nano, vol.CreatedAt); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}

	err = w.k8s.SubResource("status").Apply(ctx,
		containersv1alpha1apply.Volume(k8sName, apiNamespace).
			WithStatus(statusApply),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume status %s: %w", k8sName, err)
	}

	return nil
}
