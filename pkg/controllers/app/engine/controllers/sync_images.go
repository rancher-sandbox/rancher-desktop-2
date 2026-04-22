// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/containerd/errdefs"
	mobyimage "github.com/moby/moby/api/types/image"
	mobyclient "github.com/moby/moby/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// imageMirrorNames returns the deterministic Image mirror names for a
// Docker image ID and its RepoTags: one name per tag (hashed over
// id+tag), or a single name hashed over id alone for a dangling image.
// The "img-" prefix and SHA-256 match the vol-<sha256(name)> scheme
// used for Volumes, keeping names short, RFC 1123 valid, and
// consistent across resource types.
//
// syncAllImages shares this helper to seed activeNames from the list
// response alone, so a transient Inspect failure cannot classify
// existing mirrors as stale.
func imageMirrorNames(id string, repoTags []string) []string {
	if len(repoTags) == 0 {
		return []string{fmt.Sprintf("img-%x", sha256.Sum256([]byte(id)))}
	}
	names := make([]string, 0, len(repoTags))
	for _, tag := range repoTags {
		names = append(names, fmt.Sprintf("img-%x",
			sha256.Sum256([]byte(id+"\x00"+tag))))
	}
	return names
}

// syncAllImages lists all Docker images, creates or updates their
// Image mirrors, and prunes stale ones. Inspect is sequential for the
// same reason as syncAllContainers; parallelise here if startup
// latency on loaded machines becomes a concern.
func (w *dockerWatcher) syncAllImages(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("docker-watcher")

	listResult, err := w.cli.ImageList(ctx, mobyclient.ImageListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list images: %w", err)
	}

	// Track which Image mirror names we create so stale ones can be pruned.
	activeNames := make(map[string]bool)

	// Log and skip per-item Inspect failures, as in syncAllContainers.
	// Seed activeNames from the list response before calling Inspect
	// so a transient Inspect failure keeps the mirror in activeNames
	// and does not misclassify it as stale below.
	var errs []error
	for _, summary := range listResult.Items {
		for _, n := range imageMirrorNames(summary.ID, summary.RepoTags) {
			activeNames[n] = true
		}
		if err := w.syncImageFromSummary(ctx, summary); err != nil {
			log.Error(err, "Skipping image during full sync", "id", summary.ID)
		}
	}

	// Remove stale Image mirrors.
	var imageMirrors containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &imageMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list Images: %w", err)
	}
	for i := range imageMirrors.Items {
		img := &imageMirrors.Items[i]
		if !activeNames[img.Name] {
			log.V(1).Info("Removing stale Image", "name", img.Name)
			if err := w.removeMirrorResource(ctx, img, img.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// syncImageFromSummary creates Image mirrors from a Docker image
// summary. NotFound races are treated as success; the stale mirror is
// pruned later by syncAllImages.
func (w *dockerWatcher) syncImageFromSummary(ctx context.Context, summary mobyimage.Summary) error {
	result, err := w.cli.ImageInspect(ctx, summary.ID)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to inspect image %s: %w", summary.ID, err)
	}
	_, err = w.applyImageFromInspect(ctx, result.InspectResponse)
	return err
}

// applyImageFromInspect creates or updates Image mirrors from a
// Docker InspectResponse: one per tag, or one for a dangling image.
// Returns the mirror names applied, so reconcileImageByID can prune
// mirrors whose tags were removed.
func (w *dockerWatcher) applyImageFromInspect(ctx context.Context, inspect mobyimage.InspectResponse) ([]string, error) {
	names := imageMirrorNames(inspect.ID, inspect.RepoTags)
	var errs []error

	if len(inspect.RepoTags) > 0 {
		for i, tag := range inspect.RepoTags {
			if err := w.applyImage(ctx,
				containersv1alpha1apply.Image(names[i], w.apiNamespace).
					WithFinalizers(mirrorFinalizer),
				imageStatusFromInspect(inspect).
					WithRepoTag(tag).
					WithNamespace(containerNamespace),
			); err != nil {
				errs = append(errs, err)
			}
		}
	} else {
		// Dangling image (no tags). status.namespace is required by
		// the CRD, so set it here too.
		if err := w.applyImage(ctx,
			containersv1alpha1apply.Image(names[0], w.apiNamespace).
				WithFinalizers(mirrorFinalizer),
			imageStatusFromInspect(inspect).
				WithNamespace(containerNamespace),
		); err != nil {
			errs = append(errs, err)
		}
	}

	return names, errors.Join(errs...)
}

// imageStatusFromInspect builds a fresh ImageStatus apply config
// from a Docker inspect response. Each call returns a new value;
// sharing one across callers would alias backing slices and maps.
func imageStatusFromInspect(inspect mobyimage.InspectResponse) *containersv1alpha1apply.ImageStatusApplyConfiguration {
	// Sort RepoDigests before applying: the field is atomic under SSA
	// and Docker does not guarantee stable order, so an unsorted apply
	// would mint a new resourceVersion on every sync.
	digests := slices.Clone(inspect.RepoDigests)
	sort.Strings(digests)
	// Config is typed as a pointer and may be nil for images
	// without embedded config (legacy or partially populated).
	var labels map[string]string
	if inspect.Config != nil {
		labels = inspect.Config.Labels
	}
	statusApply := containersv1alpha1apply.ImageStatus().
		WithID(inspect.ID).
		WithRepoDigests(digests...).
		WithArchitecture(inspect.Architecture).
		WithOS(inspect.Os).
		WithSize(inspect.Size).
		WithLabels(labels)

	if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
		statusApply.WithCreatedAt(metav1.NewTime(t))
	}
	return statusApply
}

// applyImage creates or updates a single `Image` mirror and its status.
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

	err = w.k8s.Status().Apply(ctx,
		containersv1alpha1apply.Image(*image.GetName(), *image.GetNamespace()).
			WithStatus(status),
		client.ForceOwnership, client.FieldOwner(controllerName))
	if err != nil {
		return fmt.Errorf("failed to apply image status %s: %w", *image.GetName(), err)
	}

	return nil
}

// reconcileImageByID re-inspects a Docker image and reconciles every
// Image mirror whose status.id matches. Present tags are re-applied;
// mirrors for removed tags are deleted. If the image is gone from
// Docker, all mirrors with that status.id are removed.
//
// This is the path for events that carry an image ID but not the tag
// name — notably Docker's untag events (see handleImageEvent).
func (w *dockerWatcher) reconcileImageByID(ctx context.Context, id string) error {
	result, err := w.cli.ImageInspect(ctx, id)
	if errdefs.IsNotFound(err) {
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
	if err := w.k8s.List(ctx, &images, client.InNamespace(w.apiNamespace)); err != nil {
		return errors.Join(applyErr, err)
	}
	var errs []error
	if applyErr != nil {
		errs = append(errs, applyErr)
	}
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

// removeImagesByID removes every Image mirror whose status.id matches
// the given Docker image ID.
//
// Matching by status.id leaves a narrow orphan window: if applyImage
// wrote the metadata Apply but the status SubResource Apply failed
// transiently, the mirror has an empty status.id and this function
// will miss it. syncAllImages sweeps the orphan on the next watcher
// restart.
func (w *dockerWatcher) removeImagesByID(ctx context.Context, id string) error {
	var images containersv1alpha1.ImageList
	if err := w.k8s.List(ctx, &images, client.InNamespace(w.apiNamespace)); err != nil {
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
