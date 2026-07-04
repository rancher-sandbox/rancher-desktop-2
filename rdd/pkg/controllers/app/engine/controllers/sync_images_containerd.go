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
	"github.com/opencontainers/go-digest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// containerdImageMirrorName returns the deterministic Image mirror name for a
// containerd image record. Image references contain '/' and ':', so they are
// never valid K8s object names; the reference is always hashed with an "img-"
// prefix matching the moby scheme. The namespace is part of the hash because
// the same reference commonly exists in several containerd namespaces (default
// and k8s.io both hold busybox after a pull in each).
func containerdImageMirrorName(ns, name string) string {
	return fmt.Sprintf("img-%x", sha256.Sum256([]byte(ns+"\x00"+name)))
}

// syncAllImages lists all containerd images across every namespace, creates or
// updates their Image mirrors, and prunes stale ones.
func (w *containerdWatcher) syncAllImages(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")

	nsNames, err := w.cli.NamespaceService().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	// Track which mirror names we create so stale mirrors can be pruned.
	activeNames := make(map[string]bool)

	// Log and skip per-item failures rather than failing the whole startup:
	// a single permanently-broken namespace or image must not pin
	// ContainerEngineReady at ConnectFailed. Structural errors below are
	// still fatal.
	var errs []error
	for _, ns := range nsNames {
		nsCtx := namespaces.WithNamespace(ctx, ns)
		imgs, err := w.cli.ListImages(nsCtx)
		if err != nil {
			log.Error(err, "Skipping namespace during full sync", "namespace", ns)
			continue
		}
		for _, img := range imgs {
			// Seed activeNames before the apply so a transient apply failure
			// cannot classify the mirror as stale.
			activeNames[containerdImageMirrorName(ns, img.Name())] = true
			if err := w.applyImageMirror(nsCtx, ns, img); err != nil {
				log.Error(err, "Skipping image during full sync", "namespace", ns, "name", img.Name())
			}
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

// syncImage loads a single containerd image and applies the corresponding
// Image mirror. NotFound is success: the image raced a concurrent delete
// between the event and the load; the ImageDelete event or the next full sync
// prunes any stale mirror.
func (w *containerdWatcher) syncImage(ctx context.Context, ns, name string) error {
	nsCtx := namespaces.WithNamespace(ctx, ns)
	img, err := w.cli.GetImage(nsCtx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get image %s: %w", name, err)
	}
	return w.applyImageMirror(nsCtx, ns, img)
}

// applyImageMirror creates or updates an Image mirror from a containerd image.
func (w *containerdWatcher) applyImageMirror(nsCtx context.Context, ns string, img containerdclient.Image) error {
	name := img.Name()
	id := img.Target().Digest.String()
	mirrorName := containerdImageMirrorName(ns, name)

	size, err := img.Size(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get image size %s: %w", name, err)
	}

	// Spec reads the OCI image config from the content store; Architecture and
	// OS are required CRD fields.
	spec, err := img.Spec(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get image spec %s: %w", name, err)
	}

	statusApply := containersv1alpha1apply.ImageStatus().
		WithNamespace(ns).
		WithID(id).
		WithArchitecture(spec.Architecture).
		WithOS(spec.OS).
		WithSize(size).
		WithLabels(spec.Config.Labels)
	if spec.Created != nil {
		statusApply.WithCreatedAt(metav1.NewTime(*spec.Created))
	}

	// A record named by a bare digest is a dangling image; leave RepoTag unset.
	// RepoDigests are not set: containerd does not track registry digests per
	// record the way Docker does.
	if _, err := digest.Parse(name); err != nil {
		statusApply.WithRepoTag(name)
	}

	// No mirror finalizer is added until containerd-side deletes land in a
	// later PR, matching the container mirrors.
	return w.mirrorClient.applyImage(nsCtx,
		containersv1alpha1apply.Image(mirrorName, w.apiNamespace),
		statusApply)
}
