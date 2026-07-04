// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// mirrorClient holds the Kubernetes-side plumbing shared by every engine
// watcher: applying and removing mirror resources and recording container
// action outcomes.
type mirrorClient struct {
	k8s client.Client
	// apiNamespace is the Kubernetes namespace where mirror resources live.
	apiNamespace string
}

// removeMirrorResource strips the finalizer from a mirror resource and
// deletes it, used when Docker has already deleted the underlying
// object. Update retries on conflict to survive a stale cache;
// NotFound counts as success. obj is a template: one DeepCopyObject
// carries name and apiNamespace through both the retry's Get target
// (each Get overwrites its contents) and the final Delete (which keys
// off name+namespace).
func (m *mirrorClient) removeMirrorResource(ctx context.Context, obj client.Object, name string) error {
	latest := obj.DeepCopyObject().(client.Object)
	latest.SetName(name)
	latest.SetNamespace(m.apiNamespace)
	key := client.ObjectKeyFromObject(latest)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := m.k8s.Get(ctx, key, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !controllerutil.RemoveFinalizer(latest, mirrorFinalizer) {
			return nil
		}
		return m.k8s.Update(ctx, latest)
	})
	if retryErr != nil {
		return fmt.Errorf("failed to remove finalizer from %s: %w", name, retryErr)
	}
	return client.IgnoreNotFound(m.k8s.Delete(ctx, latest))
}

// patchContainerLastAction writes status.lastAction with retry-on-conflict
// and returns the updated Container. The main engine sync writes the
// status subresource on every reconcile, so this write races against it.
// If the mirror is deleted concurrently, any step may return NotFound;
// the caller treats a nil Container as "nothing left to do".
func (m *mirrorClient) patchContainerLastAction(ctx context.Context, id string, lastAction *containersv1alpha1.ContainerLastAction) (*containersv1alpha1.Container, error) {
	key := client.ObjectKey{Name: id, Namespace: m.apiNamespace}
	var result *containersv1alpha1.Container
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest containersv1alpha1.Container
		if err := m.k8s.Get(ctx, key, &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		patch := client.MergeFromWithOptions(latest.DeepCopy(), client.MergeFromWithOptimisticLock{})
		latest.Status.LastAction = lastAction
		if err := m.k8s.Status().Patch(ctx, &latest, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		result = &latest
		return nil
	})
	return result, err
}

// removeActionAnnotation clears the AnnotationAction annotation only if
// its value still matches the action just processed. A concurrent writer
// may replace the annotation with a different action between dispatch
// and cleanup; that new value must survive so the next reconcile picks
// it up. Callers pass either a fresh Container from patchContainerLastAction
// (which bypasses the informer cache, not yet showing the preceding status
// write) or a cached one (which may 409 on the first Patch). On conflict,
// the retry re-reads from the cache; by then it has usually caught up.
func (m *mirrorClient) removeActionAnnotation(ctx context.Context, latest *containersv1alpha1.Container, observed string) error {
	firstAttempt := true
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if !firstAttempt {
			if err := m.k8s.Get(ctx, client.ObjectKeyFromObject(latest), latest); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
		}
		firstAttempt = false
		current, present := latest.Annotations[containersv1alpha1.AnnotationAction]
		if !present || current != observed {
			return nil
		}
		patch := client.MergeFromWithOptions(latest.DeepCopy(), client.MergeFromWithOptimisticLock{})
		delete(latest.Annotations, containersv1alpha1.AnnotationAction)
		if err := m.k8s.Patch(ctx, latest, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		return nil
	})
}
