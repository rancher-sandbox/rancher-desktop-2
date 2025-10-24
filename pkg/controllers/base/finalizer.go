// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package base provides shared utilities for RDD controllers.
package base

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// FinalizerName is the shared finalizer name used by all RDD controllers for deletion protection.
// This finalizer blocks resource deletion until cleanup work is complete.
// Owner references indicate which resources need cleanup; the finalizer ensures cleanup happens
// before deletion proceeds. This acts as RDD's replacement for Kubernetes garbage collection.
const FinalizerName = "rdd.rancherdesktop.io/cleanup"

// EnsureFinalizer adds the finalizer to the object if it's not already present.
// Returns true if the finalizer was added and the object has been updated.
// When true is returned, controllers should return immediately to allow re-reconciliation
// with the updated object (avoids stale resourceVersion conflicts).
func EnsureFinalizer(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	if !controllerutil.ContainsFinalizer(obj, FinalizerName) {
		controllerutil.AddFinalizer(obj, FinalizerName)
		if err := c.Update(ctx, obj); err != nil {
			return false, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// RemoveFinalizer removes the finalizer from the object and updates it.
func RemoveFinalizer(ctx context.Context, c client.Client, obj client.Object) error {
	controllerutil.RemoveFinalizer(obj, FinalizerName)
	if err := c.Update(ctx, obj); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return nil
}

// IsBeingDeleted checks if an object is being deleted (has deletion timestamp).
func IsBeingDeleted(obj client.Object) bool {
	return obj.GetDeletionTimestamp() != nil
}

// ResourceNamespace is an optional interface that cluster-scoped resources can implement
// to specify which namespace contains their managed namespaced resources.
// This enables automatic namespace detection during cleanup.
type ResourceNamespace interface {
	GetResourceNamespace() string
}

// CleanupOptions provides configuration for resource cleanup.
type CleanupOptions struct {
	// ResourceLists is a slice of empty list objects used to query different resource types.
	// Each list object (e.g., &corev1.ConfigMapList{}) serves as a template that gets
	// populated by Client.List(). Example: []client.ObjectList{&corev1.ConfigMapList{}, &corev1.SecretList{}}
	ResourceLists []client.ObjectList
	// LabelSelector to find resources to clean up (optional).
	// If provided, uses efficient label-based filtering.
	// If empty, lists all resources in namespace and filters by owner UID.
	LabelSelector map[string]string
}

// DeleteOwnedResources finds and deletes all resources owned by the given object.
// This is RDD's replacement for Kubernetes garbage collection.
//
// The function always removes any finalizers from owned resources before deletion
// to ensure complete cleanup when the owner is being deleted.
//
// The function attempts to delete all owned resources, collecting errors along the way.
// If any deletions fail, it returns a combined error, but continues trying to delete
// remaining resources to make maximum progress per reconciliation.
//
// Namespace detection:
// - For namespaced owners: uses owner.GetNamespace()
// - For cluster-scoped owners: uses ResourceNamespace interface if implemented
// - Returns error if namespace cannot be determined
//
// Discovery methods:
// - If opts.LabelSelector is provided, uses efficient label-based filtering
// - If opts.LabelSelector is empty, lists all resources in namespace and filters by owner UID
func DeleteOwnedResources(ctx context.Context, c client.Client, owner client.Object, opts CleanupOptions) error {
	logger := log.FromContext(ctx)

	var namespace string
	if ns := owner.GetNamespace(); ns != "" {
		namespace = ns
	} else if rn, ok := owner.(ResourceNamespace); ok {
		namespace = rn.GetResourceNamespace()
	} else {
		return fmt.Errorf("cannot determine namespace for cleanup: owner %q is cluster-scoped but does not implement ResourceNamespace interface", owner.GetName())
	}

	var listOpts = []client.ListOption{client.InNamespace(namespace)}
	if len(opts.LabelSelector) > 0 {
		listOpts = append(listOpts, client.MatchingLabels(opts.LabelSelector))
	}
	var errs []error

	for _, resourceList := range opts.ResourceLists {
		// List() mutates resourceList by populating its Items field
		if err := c.List(ctx, resourceList, listOpts...); err != nil {
			errs = append(errs, fmt.Errorf("failed to list resources for cleanup: %w", err))
			continue // Try next resource type
		}

		// Use Kubernetes standard library to iterate over list items
		_ = meta.EachListItem(resourceList, func(runtimeObj runtime.Object) error {
			obj, ok := runtimeObj.(client.Object)
			if !ok {
				errs = append(errs, fmt.Errorf("item is not a client.Object: %T", runtimeObj))
				return nil // Continue to next item
			}
			if !isOwnedByUID(obj, owner.GetUID()) {
				return nil // Skip this item, continue iteration
			}

			// Remove all finalizers before deletion to ensure cleanup completes
			if len(obj.GetFinalizers()) > 0 {
				logger.Info("Removing finalizers from owned resource",
					"resourceType", fmt.Sprintf("%T", obj),
					"resourceNamespace", obj.GetNamespace(),
					"resourceName", obj.GetName(),
					"finalizers", obj.GetFinalizers())

				obj.SetFinalizers([]string{})
				if err := c.Update(ctx, obj); err != nil {
					errs = append(errs, fmt.Errorf("failed to remove finalizers from %q: %w", obj.GetName(), err))
					return nil // Continue to next item
				}
			}

			logger.Info("Deleting owned resource",
				"resourceType", fmt.Sprintf("%T", obj),
				"resourceNamespace", obj.GetNamespace(),
				"resourceName", obj.GetName())

			if err := c.Delete(ctx, obj); err != nil && client.IgnoreNotFound(err) != nil {
				errs = append(errs, fmt.Errorf("failed to delete resource %q during cleanup: %w", obj.GetName(), err))
			}
			return nil // Always continue to next item
		})
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to delete some owned resources (%d errors): %v", len(errs), errs)
	}
	return nil
}

// HasFinalizer checks if a resource has the RDD finalizer.
func HasFinalizer(obj client.Object) bool {
	return controllerutil.ContainsFinalizer(obj, FinalizerName)
}

// isOwnedByUID checks if a resource is owned by an owner with the given UID.
func isOwnedByUID(obj client.Object, ownerUID types.UID) bool {
	for _, ownerRef := range obj.GetOwnerReferences() {
		if ownerRef.UID == ownerUID {
			return true
		}
	}
	return false
}
