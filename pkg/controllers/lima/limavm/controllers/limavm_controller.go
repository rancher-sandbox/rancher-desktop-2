// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	// TemplateConfigMapLabel is the label applied to template ConfigMaps managed by LimaVM resources.
	TemplateConfigMapLabel = "lima.rancherdesktop.io/template-configmap"
)

// LimaVMReconciler reconciles a LimaVM object.
type LimaVMReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// Webhook and reconciler responsibilities:
// - Mutating webhook adds finalizer to LimaVM during admission
// - Mutating webhook validates templateRef and creates ConfigMap with finalizer
// - Reconciler sets owner reference (requires LimaVM UID which is only available after persistence)
// - Reconciler sets status.templateConfigMap after owner reference is set
// - ConfigMap admission webhook validates template content and prevents deletion
//
// The status.templateConfigMap field serves dual purposes:
// - Informational: external consumers can read which ConfigMap contains the template
// - Optimization: indicates owner reference setup is complete, avoiding unnecessary fetches
//
// Note: templateRef is never accessed after initial creation (handled by mutating webhook).
// It exists only as documentation of the template's origin.
func (r *LimaVMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the LimaVM instance
	var limaVM v1alpha1.LimaVM
	if err := r.Get(ctx, req.NamespacedName, &limaVM); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("LimaVM resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get LimaVM")
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&limaVM) {
		return r.handleDeletion(ctx, &limaVM)
	}

	// If status.templateConfigMap is already set then initialization is complete.
	if limaVM.Status.TemplateConfigMap != "" {
		return ctrl.Result{}, nil
	}

	// Fetch the template ConfigMap to set owner reference
	// The mutating webhook creates it during admission, but cannot set the owner reference
	// (requires LimaVM UID which is only available after the resource is persisted)
	configMapName := limaVM.GetTemplateConfigMapName()
	templateConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: limaVM.Namespace}, templateConfigMap); err != nil {
		logger.Error(err, "Failed to get template ConfigMap for owner reference setup")
		return ctrl.Result{}, err
	}

	// Set owner reference if not already set.
	// Note: ConfigMap finalizer is already set during creation by the mutating webhook.
	if len(templateConfigMap.OwnerReferences) == 0 {
		if err := ctrl.SetControllerReference(&limaVM, templateConfigMap, r.Scheme); err != nil {
			logger.Error(err, "Failed to set owner reference on template ConfigMap")
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, templateConfigMap); err != nil {
			logger.Error(err, "Failed to update template ConfigMap")
			return ctrl.Result{}, err
		}
		logger.Info("Set owner reference on template ConfigMap", "ConfigMap.Name", configMapName)
	}

	// Update status.templateConfigMap to mark initialization as complete
	limaVM.Status.TemplateConfigMap = limaVM.GetTemplateConfigMapName()
	if err := r.Status().Update(ctx, &limaVM); err != nil {
		logger.Error(err, "Failed to update status")
		return ctrl.Result{}, err
	}
	logger.Info("Completed initialization: set owner reference and status", "ConfigMap.Name", limaVM.GetTemplateConfigMapName())

	return ctrl.Result{}, nil
}

// handleDeletion handles cleanup when a LimaVM is being deleted.
func (r *LimaVMReconciler) handleDeletion(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Clean up all owned resources using the base helper
	// DeleteOwnedResources automatically removes any finalizers before deletion
	cleanupOpts := base.CleanupOptions{
		ResourceLists: []client.ObjectList{
			&corev1.ConfigMapList{},
		},
	}
	if err := base.DeleteOwnedResources(ctx, r.Client, limaVM, cleanupOpts); err != nil {
		logger.Error(err, "Failed to delete owned resources")
		return ctrl.Result{}, err
	}

	// Remove finalizer to allow LimaVM deletion
	if err := base.RemoveFinalizer(ctx, r.Client, limaVM); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully deleted owned resources and removed finalizer")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *LimaVMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LimaVM{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
