// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	mobyimage "github.com/moby/moby/api/types/image"
	dockerclient "github.com/moby/moby/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// sanitizeKubernetesObjectName replaces characters not allowed in K8s names.
func sanitizeKubernetesObjectName(input string) string {
	return strings.NewReplacer("/", "-", ":", ".").Replace(input)
}

// syncAllImages lists all Docker images and creates/updates K8s resources,
// then removes stale ones.
//
// Images are Inspected sequentially for the same reason as
// syncAllContainers: one-shot at startup, typical dev machines have
// tens of images, and we need fields that only Inspect exposes
// (RepoDigests, full Labels, detailed metadata). Parallelise here if
// startup latency becomes a concern.
func (w *dockerWatcher) syncAllImages(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ImageList(ctx, dockerclient.ImageListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	// Track which K8s resource names we create so we can prune stale ones.
	activeNames := make(map[string]bool)

	var errs []error
	for _, summary := range listResult.Items {
		names, err := w.syncImageFromSummary(ctx, summary)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, n := range names {
			activeNames[n] = true
		}
	}

	// Remove stale K8s images.
	var k8sImages containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &k8sImages, client.InNamespace(apiNamespace)); err != nil {
		return fmt.Errorf("failed to list K8s images: %w", err)
	}
	for i := range k8sImages.Items {
		img := &k8sImages.Items[i]
		if !activeNames[img.Name] {
			log.V(1).Info("Removing stale image", "name", img.Name)
			if err := w.removeMirrorResource(ctx, img, img.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncImageFromSummary creates K8s resources from a Docker image summary.
// Returns the K8s resource names that were created. NotFound races
// during fullSync are treated as success; the stale K8s mirror is
// pruned later by syncAllImages' remove-stale step.
func (w *dockerWatcher) syncImageFromSummary(ctx context.Context, summary mobyimage.Summary) ([]string, error) {
	result, err := w.cli.ImageInspect(ctx, summary.ID)
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to inspect image %s: %w", summary.ID, err)
	}
	return w.applyImageFromInspect(ctx, result.InspectResponse)
}

// applyImageFromInspect creates or updates Image resources from a Docker
// InspectResponse. One resource per tag, plus one for dangling images.
// Returns the K8s resource names that were created.
func (w *dockerWatcher) applyImageFromInspect(ctx context.Context, inspect mobyimage.InspectResponse) ([]string, error) {
	var names []string
	var errs []error

	if len(inspect.RepoTags) > 0 {
		for _, tag := range inspect.RepoTags {
			// Deterministic name from image ID + tag hash (same as mock controller).
			name := fmt.Sprintf("%s-%x",
				sanitizeKubernetesObjectName(inspect.ID),
				sha256.Sum256([]byte(tag)))
			names = append(names, name)

			if err := w.applyImage(ctx,
				containersv1alpha1apply.Image(name, apiNamespace).
					WithFinalizers(mirrorFinalizer),
				imageStatusFromInspect(inspect).
					WithRepoTag(tag).
					WithNamespace(containerNamespace),
			); err != nil {
				errs = append(errs, err)
			}
		}
	} else {
		// Dangling image (no tags).
		name := sanitizeKubernetesObjectName(inspect.ID)
		names = append(names, name)
		if err := w.applyImage(ctx,
			containersv1alpha1apply.Image(name, apiNamespace).
				WithFinalizers(mirrorFinalizer),
			imageStatusFromInspect(inspect),
		); err != nil {
			errs = append(errs, err)
		}
	}

	return names, errors.Join(errs...)
}

// imageStatusFromInspect builds a fresh ImageStatus apply config from a
// Docker inspect response. Returning a new value per call avoids the
// aliasing trap of a shallow struct copy — slice/map WithX calls on
// one "copy" would otherwise mutate the backing memory shared with
// other callers.
func imageStatusFromInspect(inspect mobyimage.InspectResponse) *containersv1alpha1apply.ImageStatusApplyConfiguration {
	statusApply := containersv1alpha1apply.ImageStatus().
		WithID(inspect.ID).
		WithRepoDigests(inspect.RepoDigests...).
		WithArchitecture(inspect.Architecture).
		WithOS(inspect.Os).
		WithSize(inspect.Size).
		WithLabels(inspect.Config.Labels)

	if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}
	return statusApply
}

// applyImage creates or updates a single Image resource and its status.
func (w *dockerWatcher) applyImage(
	ctx context.Context,
	image *containersv1alpha1apply.ImageApplyConfiguration,
	status *containersv1alpha1apply.ImageStatusApplyConfiguration,
) error {
	err := w.k8s.Apply(ctx, image,
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply image %s: %w", *image.GetName(), err)
	}

	err = w.k8s.SubResource("status").Apply(ctx,
		containersv1alpha1apply.Image(*image.GetName(), *image.GetNamespace()).
			WithStatus(status),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply image status %s: %w", *image.GetName(), err)
	}

	return nil
}

// reconcileImageByID re-inspects an image and reconciles every K8s Image
// mirror whose status.id matches. Tags still present are re-applied; K8s
// resources for tags that are no longer present are deleted. If the
// image has been fully removed, all mirrors with that status.id are
// deleted instead.
//
// This is the path for events that carry an image ID but not the tag
// name — notably Docker's untag events, where the event payload only
// contains the image ID (see handleImageEvent).
func (w *dockerWatcher) reconcileImageByID(ctx context.Context, id string) error {
	result, err := w.cli.ImageInspect(ctx, id)
	if cerrdefs.IsNotFound(err) {
		return w.removeImagesByID(ctx, id)
	}
	if err != nil {
		return fmt.Errorf("failed to inspect image %s: %w", id, err)
	}

	freshNames, applyErr := w.applyImageFromInspect(ctx, result.InspectResponse)
	keep := make(map[string]bool, len(freshNames))
	for _, n := range freshNames {
		keep[n] = true
	}

	var images containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return errors.Join(applyErr, err)
	}
	errs := []error{applyErr}
	for i := range images.Items {
		img := &images.Items[i]
		if img.Status.ID != result.InspectResponse.ID {
			continue
		}
		if keep[img.Name] {
			continue
		}
		if err := w.removeMirrorResource(ctx, img, img.Name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeImagesByID finds and removes all Image resources for a given image ID.
func (w *dockerWatcher) removeImagesByID(ctx context.Context, id string) error {
	var images containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &images, client.InNamespace(apiNamespace)); err != nil {
		return err
	}
	var errs []error
	for i := range images.Items {
		if images.Items[i].Status.ID == id {
			if err := w.removeMirrorResource(ctx, &images.Items[i], images.Items[i].Name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
