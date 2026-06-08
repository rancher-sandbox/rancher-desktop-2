// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Namespace reconciler, which handles
// namespace deletion by deleting all resources within the namespace and
// removing the kubernetes finalizer.
package controllers

import (
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	// kubernetesFinalizer is the standard Kubernetes namespace finalizer.
	// This finalizer is automatically added to all namespaces by the upstream NamespaceLifecycle
	// admission plugin (enabled in pkg/service/admission/config.go). The admission plugin
	// adds it during CREATE, ensuring all namespaces (including system namespaces like
	// default and rdd-system) have the finalizer from the start.
	//
	// This controller is responsible for removing the finalizer after cleaning up all
	// resources in the namespace during deletion.
	kubernetesFinalizer = "kubernetes"
)

// NamespaceReconciler reconciles Namespace objects to handle deletion lifecycle.
//
// This controller replicates the behavior of the standard Kubernetes namespace controller,
// which RDD cannot use because controller-manager requires kubelet.
//
// Namespace deletion in Kubernetes is a two-part process:
// 1. NamespaceLifecycle admission plugin adds the "kubernetes" finalizer during CREATE.
// 2. Namespace controller (this controller) deletes all resources and removes the finalizer.
type NamespaceReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Manager ctrl.Manager
}

// Reconcile implements the reconciliation loop for namespace deletion.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// Fetch the namespace
	var namespace corev1.Namespace
	if err := r.Get(ctx, req.NamespacedName, &namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !base.IsBeingDeleted(&namespace) {
		return ctrl.Result{}, nil
	}
	// Check if the namespace has the kubernetes finalizer
	// Can't use controllerutil.ContainsFinalizer() because namespaces use Spec.Finalizers instead of Metadata.Finalizers.
	if !slices.Contains(namespace.Spec.Finalizers, kubernetesFinalizer) {
		return ctrl.Result{}, nil
	}

	log.Info("Processing namespace deletion", "namespace", namespace.Name)

	resourceTypes, err := base.DiscoverNamespacedResources(ctx, r.Manager)
	if err != nil {
		log.Error(err, "Failed to discover namespaced resources")
		return ctrl.Result{}, err
	}

	// Loop internally until all resources are deleted or no progress can be made.
	// This avoids re-queueing and rate limiter delays when making progress.
	previousCount := -1 // Start with -1 to indicate first iteration

	for {
		remainingCount, err := r.deleteAllResources(ctx, namespace.Name, resourceTypes)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Check if all resources are deleted
		if remainingCount == 0 {
			break
		}

		// Check if we made progress
		if previousCount > 0 && remainingCount >= previousCount {
			log.Error(nil, "Namespace deletion stuck - no progress made",
				"namespace", namespace.Name,
				"remaining", remainingCount)
			// Requeue with longer delay to give finalizers more time
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		previousCount = remainingCount
		// Brief delay to allow API server to process delete requests
		time.Sleep(100 * time.Millisecond)
	}

	// All resources deleted, remove the kubernetes finalizer
	// Re-fetch the namespace to get the latest resourceVersion
	if err := r.Get(ctx, req.NamespacedName, &namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := r.removeFinalizer(ctx, &namespace, kubernetesFinalizer); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// deleteAllResources deletes resources from the namespace.
// Returns the count of remaining resources.
func (r *NamespaceReconciler) deleteAllResources(ctx context.Context, namespaceName string, resourceTypes []schema.GroupVersionKind) (int, error) {
	// IMPORTANT: This function uses GetAPIReader() instead of the cached client (r.Client) because:
	// - The cached client automatically sets up informers/watches for every resource type it lists
	// - With dynamic discovery, we don't know what resource types exist until runtime
	// - The controller manager's scheme may not have all discovered types registered for watching
	// - Attempting to list unknown types with the cached client causes the reflector to fail
	// - GetAPIReader() bypasses the cache and reads directly from the API server, avoiding watches
	apiReader := r.Manager.GetAPIReader()

	totalRemaining := 0
	for _, gvk := range resourceTypes {
		list := &metav1.PartialObjectMetadataList{}
		list.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   gvk.Group,
			Version: gvk.Version,
			Kind:    gvk.Kind + "List",
		})

		// List resources of this type in the namespace using API reader (bypasses cache)
		if err := apiReader.List(ctx, list, client.InNamespace(namespaceName)); err != nil {
			continue
		}

		if len(list.Items) > 0 {
			// Try to delete each resource
			for _, item := range list.Items {
				_ = r.Delete(ctx, &item) // Ignore errors, will retry on next iteration
			}

			// Re-list to count remaining
			listAfter := &metav1.PartialObjectMetadataList{}
			listAfter.SetGroupVersionKind(list.GroupVersionKind())
			if err := apiReader.List(ctx, listAfter, client.InNamespace(namespaceName)); err == nil {
				totalRemaining += len(listAfter.Items)
			}
		}
	}

	return totalRemaining, nil
}

// removeFinalizer removes the specified finalizer from the namespace.
// IMPORTANT: Namespaces require using the "finalize" subresource to update spec.finalizers.
// A regular Update() call will appear to succeed but won't actually persist the change.
func (r *NamespaceReconciler) removeFinalizer(ctx context.Context, namespace *corev1.Namespace, finalizer string) error {
	namespace.Spec.Finalizers = slices.DeleteFunc(namespace.Spec.Finalizers, func(f corev1.FinalizerName) bool {
		return string(f) == finalizer
	})
	if err := r.SubResource("finalize").Update(ctx, namespace); err != nil {
		return fmt.Errorf("failed to remove namespace finalizer: %w", err)
	}
	return nil
}
