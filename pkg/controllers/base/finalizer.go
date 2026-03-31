// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CleanupFinalizerName is the self-protection finalizer. A resource's own controller sets it
// to ensure cleanup runs before deletion (e.g., LimaVM stops the VM and deletes disk files).
// DeleteOwnedResources does NOT strip this finalizer.
const CleanupFinalizerName = "rdd.rancherdesktop.io/cleanup"

// ownedFinalizerPrefix is the prefix for cascade-blocking finalizers. The full finalizer
// name includes the owner's Kind so that the validating webhook can tell the user
// which resource to delete instead (e.g., "rdd.rancherdesktop.io/owned-by-App").
const ownedFinalizerPrefix = "rdd.rancherdesktop.io/owned-by-"

// OwnedFinalizerFor returns the owned finalizer name for a given owner Kind.
func OwnedFinalizerFor(ownerKind string) string {
	return ownedFinalizerPrefix + ownerKind
}

// EnsureCleanupFinalizer adds the cleanup finalizer to the object if not already present.
// Returns true if the finalizer was added and the object has been updated.
// When true is returned, controllers should return immediately to allow re-reconciliation
// with the updated object (avoids stale resourceVersion conflicts).
func EnsureCleanupFinalizer(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	if !controllerutil.AddFinalizer(obj, CleanupFinalizerName) {
		return false, nil
	}
	if err := c.Update(ctx, obj); err != nil {
		return false, fmt.Errorf("failed to add cleanup finalizer: %w", err)
	}
	return true, nil
}

// RemoveCleanupFinalizer removes the cleanup finalizer from the object and updates it.
func RemoveCleanupFinalizer(ctx context.Context, c client.Client, obj client.Object) error {
	if controllerutil.RemoveFinalizer(obj, CleanupFinalizerName) {
		if err := c.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to remove cleanup finalizer: %w", err)
		}
	}
	return nil
}

// EnsureOwnedFinalizer adds the owned finalizer for ownerKind to a child object if not
// already present. Returns true if the finalizer was added and the object has been updated.
// A resource may have at most one owned finalizer; adding a second is a programming error.
func EnsureOwnedFinalizer(ctx context.Context, c client.Client, obj client.Object, ownerKind string) (bool, error) {
	// Belt-and-suspenders: controllerutil.SetControllerReference already rejects a second
	// controller owner, so this should never trigger. Guard anyway for a clear error message.
	if existing := OwnedFinalizerOwner(obj); existing != "" && existing != ownerKind {
		return false, fmt.Errorf("cannot add owned-by-%s finalizer: already owned by %s (multi-owner not supported)", ownerKind, existing)
	}
	if !controllerutil.AddFinalizer(obj, OwnedFinalizerFor(ownerKind)) {
		return false, nil
	}
	if err := c.Update(ctx, obj); err != nil {
		return false, fmt.Errorf("failed to add owned finalizer: %w", err)
	}
	return true, nil
}

// RemoveOwnedFinalizer removes the owned finalizer for ownerKind from the object and updates it.
func RemoveOwnedFinalizer(ctx context.Context, c client.Client, obj client.Object, ownerKind string) error {
	if controllerutil.RemoveFinalizer(obj, OwnedFinalizerFor(ownerKind)) {
		if err := c.Update(ctx, obj); err != nil {
			return fmt.Errorf("failed to remove owned finalizer: %w", err)
		}
	}
	return nil
}

// IsBeingDeleted checks if an object is being deleted (has non-zero deletion timestamp).
func IsBeingDeleted(obj client.Object) bool {
	return !obj.GetDeletionTimestamp().IsZero()
}

// ResourceNamespace is an optional interface that cluster-scoped resources can implement
// to specify which namespace contains their managed namespaced resources.
// This enables automatic namespace detection during cleanup.
type ResourceNamespace interface {
	GetResourceNamespace() string
}

// DeleteOwnedResources finds and deletes all resources owned by the given object.
// This is RDD's replacement for Kubernetes garbage collection.
//
// The function uses dynamic resource discovery to automatically find ALL namespaced
// resource types in the cluster, eliminating the need to manually specify resource lists.
//
// The function attempts to delete all owned resources, collecting errors along the way.
// If any deletions fail, it returns a combined error, but continues trying to delete
// remaining resources to make maximum progress per reconciliation.
//
// DeleteOwnedResources only looks for owned resources in the owner.GetNamespace() namespace.
// For cluster-scoped objects it uses the ResourceNamespace interface to determine the namespace.
//
// Use opts.LabelSelector to efficiently filter the set of deletion candidates, if possible.
// Resources not actually owned by the owner will not be touched.
func DeleteOwnedResources(ctx context.Context, c client.Client, owner client.Object, mgr ctrl.Manager) error {
	logger := log.FromContext(ctx)

	var namespace string
	if ns := owner.GetNamespace(); ns != "" {
		namespace = ns
	} else if rn, ok := owner.(ResourceNamespace); ok {
		namespace = rn.GetResourceNamespace()
	} else {
		return fmt.Errorf("cannot determine namespace for cleanup: owner %q is cluster-scoped %s resource but does not implement ResourceNamespace interface",
			owner.GetName(), owner.GetObjectKind().GroupVersionKind())
	}

	// Discover all namespaced resource types dynamically
	resourceTypes, err := DiscoverNamespacedResources(ctx, mgr)
	if err != nil {
		return fmt.Errorf("failed to discover namespaced resources: %w", err)
	}

	// Use GetAPIReader() instead of cached client for listing resources to avoid
	// setting up watches for dynamically discovered types that may not be in the scheme.
	apiReader := mgr.GetAPIReader()
	var errs []error
	listOpts := []client.ListOption{client.InNamespace(namespace)}

	for _, gvk := range resourceTypes {
		// We use PartialObjectMetadata because we're doing dynamic resource discovery -
		// we don't know at compile time what types we'll encounter, and those types may
		// not be registered in the scheme. PartialObjectMetadata lets us work with ANY
		// Kubernetes resource using just metadata fields (name, namespace, finalizers, etc.)
		// without needing the full type definition.
		list := &metav1.PartialObjectMetadataList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind + "List",
		})

		// List resources of this type in the namespace using API reader (bypasses cache)
		if err := apiReader.List(ctx, list, listOpts...); err != nil {
			logger.V(1).Info("Failed to list resources, skipping",
				"gvk", gvk.String(),
				"error", err.Error())
			continue
		}

		// Process each resource
		for _, item := range list.Items {
			if !IsOwnedByUID(&item, owner.GetUID()) {
				continue
			}

			// Strip owned finalizers to unblock deletion. The cleanup finalizer (if any)
			// remains so the child's own controller can still run its cleanup logic.
			patch := client.MergeFrom(item.DeepCopy())
			if removeOwnedFinalizers(&item) {
				// Use Patch instead of Update for PartialObjectMetadata
				if err := c.Patch(ctx, &item, patch); err != nil {
					itemLogger := logger.V(1).WithValues("namespace", item.GetNamespace(), "name", item.GetName(), "gvk", gvk.String())
					if apierrors.IsNotFound(err) {
						itemLogger.Info("Resource already deleted during finalizer removal")
						continue
					}
					// For other errors, log and collect error but proceed with deletion attempt
					// The deletion might still succeed if there are no other blocking finalizers
					itemLogger.Info("Failed to remove finalizer, will attempt deletion anyway", "error", err)
					errs = append(errs, fmt.Errorf("failed to remove finalizers from %s %s/%s: %w", gvk, item.GetNamespace(), item.GetName(), err))
				}
			}
			if err := c.Delete(ctx, &item); err != nil && client.IgnoreNotFound(err) != nil {
				errs = append(errs, fmt.Errorf("failed to delete %s %s/%s during cleanup: %w", gvk, item.GetNamespace(), item.GetName(), err))
			}
		}
	}
	return errors.Join(errs...)
}

// DiscoverNamespacedResources discovers all namespaced resource types in the cluster.
// Uses the Kubernetes discovery API to find all available namespaced resources dynamically.
func DiscoverNamespacedResources(ctx context.Context, mgr ctrl.Manager) ([]schema.GroupVersionKind, error) {
	logger := log.FromContext(ctx)

	// Create discovery client from manager's config
	config := mgr.GetConfig()
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client: %w", err)
	}

	// Get all server resources
	_, apiResourceLists, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		// ServerGroupsAndResources can return partial results with an error
		// We'll use whatever we got and log the error
		logger.V(1).Info("Discovery returned partial results", "error", err)
	}

	var resourceTypes []schema.GroupVersionKind

	// Iterate through all API groups and resources
	for _, apiResourceList := range apiResourceLists {
		// Parse the GroupVersion from the list
		gv, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			logger.V(1).Info("Failed to parse group version, skipping",
				"groupVersion", apiResourceList.GroupVersion,
				"error", err)
			continue
		}
		for _, apiResource := range apiResourceList.APIResources {
			if !apiResource.Namespaced {
				continue
			}
			// Skip subresources (e.g., namespaces/status, namespaces/finalize)
			if strings.Contains(apiResource.Name, "/") {
				continue
			}
			gvk := schema.GroupVersionKind{
				Group:   gv.Group,
				Version: gv.Version,
				Kind:    apiResource.Kind,
			}
			resourceTypes = append(resourceTypes, gvk)
		}
	}

	logger.V(1).Info("Discovered namespaced resources for cleanup",
		"totalResources", len(resourceTypes),
		"apiGroups", len(apiResourceLists))

	return resourceTypes, nil
}

// HasCleanupFinalizer checks if a resource has the cleanup finalizer.
func HasCleanupFinalizer(obj client.Object) bool {
	return controllerutil.ContainsFinalizer(obj, CleanupFinalizerName)
}

// HasOwnedFinalizer checks if a resource has any owned finalizer.
func HasOwnedFinalizer(obj client.Object) bool {
	return OwnedFinalizerOwner(obj) != ""
}

// OwnedFinalizerOwner returns the owner Kind encoded in the owned finalizer,
// or "" if no owned finalizer is present.
func OwnedFinalizerOwner(obj client.Object) string {
	for _, f := range obj.GetFinalizers() {
		if strings.HasPrefix(f, ownedFinalizerPrefix) {
			return strings.TrimPrefix(f, ownedFinalizerPrefix)
		}
	}
	return ""
}

// removeOwnedFinalizers strips all owned-by-* finalizers from obj.
// Returns true if any were removed. Multi-owner is not supported
// (EnsureOwnedFinalizer rejects it), so at most one will be present.
func removeOwnedFinalizers(obj client.Object) bool {
	finalizers := obj.GetFinalizers()
	filtered := make([]string, 0, len(finalizers))
	for _, f := range finalizers {
		if !strings.HasPrefix(f, ownedFinalizerPrefix) {
			filtered = append(filtered, f)
		}
	}
	if len(filtered) == len(finalizers) {
		return false
	}
	obj.SetFinalizers(filtered)
	return true
}

// IsOwnedByUID checks if a resource is owned by an owner with the given UID.
func IsOwnedByUID(obj client.Object, ownerUID types.UID) bool {
	for _, ownerRef := range obj.GetOwnerReferences() {
		if ownerRef.UID == ownerUID {
			return true
		}
	}
	return false
}
