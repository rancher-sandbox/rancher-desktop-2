// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/errdefs"
	mobyvolume "github.com/moby/moby/api/types/volume"
	mobyclient "github.com/moby/moby/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/api"
)

const volumeMirrorPrefix = "vol"

// syncAllVolumes lists all Docker volumes, creates or updates their
// Volume mirrors, and prunes stale ones.
func (w *dockerWatcher) syncAllVolumes(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	volumeList, err := w.cli.VolumeList(ctx, mobyclient.VolumeListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list volumes: %w", err)
	}

	// Track which `Volume` mirror names we create.
	activeNames := make(map[string]bool, len(volumeList.Items))

	// Log and skip per-item Apply failures, matching the
	// containers/images pattern: a single permanently-broken Apply
	// must not pin ContainerEngineReady at ConnectFailed. Structural
	// errors below are still fatal.
	var errs []error
	for _, v := range volumeList.Items {
		mirrorName := api.MirrorName(volumeMirrorPrefix, v.Name)
		activeNames[mirrorName] = true
		if err := w.applyVolume(ctx, v); err != nil {
			log.Error(err, "Skipping volume during full sync", "name", v.Name)
		}
	}

	// Remove stale Volume mirrors.
	var volumeMirrors containersv1alpha1.VolumeList
	if err := w.k8s.List(ctx, &volumeMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Volumes: %w", err)
	}
	for i := range volumeMirrors.Items {
		vol := &volumeMirrors.Items[i]
		if !activeNames[vol.Name] {
			log.V(1).Info("Removing stale Volume", "name", vol.Name)
			if err := w.removeMirrorResource(ctx, vol, vol.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncVolume inspects a single Docker volume and applies the
// corresponding Volume mirror. NotFound is success: the volume raced
// a concurrent delete between List and Inspect, and the stale mirror
// is pruned later in syncAllVolumes.
func (w *dockerWatcher) syncVolume(ctx context.Context, name string) error {
	result, err := w.cli.VolumeInspect(ctx, name, mobyclient.VolumeInspectOptions{})
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect volume %s: %w", name, err)
	}
	return w.applyVolume(ctx, result.Volume)
}

// applyVolume creates or updates a `Volume` mirror from a Docker volume.
func (w *dockerWatcher) applyVolume(ctx context.Context, vol mobyvolume.Volume) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")
	mirrorName := api.MirrorName(volumeMirrorPrefix, vol.Name)

	applyConfig := containersv1alpha1apply.Volume(mirrorName, w.apiNamespace).
		WithFinalizers(mirrorFinalizer)

	err := w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume %s: %w", mirrorName, err)
	}

	statusApply := containersv1alpha1apply.VolumeStatus().
		WithName(vol.Name).
		WithNamespace(containerNamespace).
		WithDriver(vol.Driver).
		WithLabels(vol.Labels).
		WithOptions(vol.Options).
		WithMountPoint(vol.Mountpoint).
		WithScope(vol.Scope)

	// Leave createdAt unset when the timestamp will not parse. A stand-in
	// such as the epoch reaches the UI as a real date, with nothing to mark
	// it as invented.
	if t, err := time.Parse(time.RFC3339Nano, vol.CreatedAt); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	} else {
		log.V(1).Info("Volume has no readable creation time",
			"volume", vol.Name, "created", vol.CreatedAt)
	}

	err = w.k8s.Status().Apply(ctx,
		containersv1alpha1apply.Volume(mirrorName, w.apiNamespace).
			WithStatus(statusApply),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply volume status %s: %w", mirrorName, err)
	}

	return nil
}
