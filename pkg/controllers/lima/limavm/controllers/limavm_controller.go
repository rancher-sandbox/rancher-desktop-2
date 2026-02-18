// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	limainstance "github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/store"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	// TemplateConfigMapLabel is the label applied to template ConfigMaps managed by LimaVM resources.
	TemplateConfigMapLabel = "lima.rancherdesktop.io/template-configmap"

	// ConditionCreated indicates whether the Lima instance has been created on disk.
	ConditionCreated = "Created"

	// ConditionRunning indicates whether the Lima instance is running.
	ConditionRunning = "Running"

	// ReasonCreated is used when the Lima instance was successfully created.
	ReasonCreated = "Created"

	// ReasonCreateFailed is used when the Lima instance creation failed.
	ReasonCreateFailed = "CreateFailed"

	// ReasonPending is used when the reconciler has seen the resource but not yet created the instance.
	ReasonPending = "Pending"

	// ReasonStarting is used when the Lima instance is starting but not yet running.
	ReasonStarting = "Starting"

	// ReasonStarted is used when the Lima instance was successfully started.
	ReasonStarted = "Started"

	// ReasonStartFailed is used when the Lima instance failed to start.
	ReasonStartFailed = "StartFailed"

	// ReasonStopped is used when the Lima instance was successfully stopped.
	ReasonStopped = "Stopped"

	// ReasonStopFailed is used when the Lima instance failed to stop.
	ReasonStopFailed = "StopFailed"

	// ReasonBroken is used when the Lima instance is in an inconsistent state.
	ReasonBroken = "Broken"

	// preparingSentinel is a marker file created during instance preparation.
	// Its presence indicates that preparation is in progress or failed.
	preparingSentinel = ".preparing"
)

// sentinelPath returns the path to the preparing sentinel file for an instance.
func sentinelPath(instanceName string) string {
	return filepath.Join(instance.LimaHome(), instanceName, preparingSentinel)
}

// hasSentinel reports whether the preparing sentinel file exists.
func hasSentinel(instanceName string) bool {
	_, err := os.Stat(sentinelPath(instanceName))
	return err == nil
}

// createSentinel creates the preparing sentinel file.
func createSentinel(instanceName string) error {
	return os.WriteFile(sentinelPath(instanceName), nil, 0o644)
}

// removeSentinel removes the preparing sentinel file.
func removeSentinel(instanceName string) error {
	err := os.Remove(sentinelPath(instanceName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// LimaVMReconciler reconciles a LimaVM object.
type LimaVMReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Manager  ctrl.Manager
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the cluster state toward the desired state.
// See docs/design/api_lima.md for a flow diagram.
//
// Webhook and reconciler responsibilities:
// - Mutating webhook: adds finalizer, validates templateRef, creates ConfigMap
// - Reconciler: sets owner reference (needs LimaVM UID, available only after persistence)
// - Reconciler: sets status.templateConfigMap, creates Lima instance on disk
// - ConfigMap webhook: validates template content, prevents deletion
//
// The status.templateConfigMap field tells consumers which ConfigMap holds the template
// and signals that owner reference setup is complete.
//
// The templateRef field documents the template's origin; the webhook handles it at creation.
func (r *LimaVMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var limaVM v1alpha1.LimaVM
	if err := r.Get(ctx, req.NamespacedName, &limaVM); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("LimaVM resource not found; already deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get LimaVM")
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&limaVM) {
		return r.handleDeletion(ctx, &limaVM)
	}

	// Set initial condition to Unknown so other components know reconciliation is in progress.
	if apimeta.FindStatusCondition(limaVM.Status.Conditions, ConditionCreated) == nil {
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionUnknown, ReasonPending, "Reconciliation in progress")
		if err := r.Status().Update(ctx, &limaVM); err != nil {
			logger.Error(err, "Failed to set initial condition")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle instances with a preparing sentinel file. The sentinel indicates that
	// a previous reconcile started preparation but didn't complete successfully.
	if hasSentinel(limaVM.Name) {
		if apimeta.IsStatusConditionTrue(limaVM.Status.Conditions, ConditionCreated) {
			// Preparation completed but sentinel wasn't cleaned up; remove it now.
			if err := removeSentinel(limaVM.Name); err != nil {
				logger.Error(err, "Failed to remove sentinel file")
				return ctrl.Result{}, err
			}
			logger.Info("Removed stale sentinel file", "instance", limaVM.Name)
			// Continue to check for further work.
		} else {
			// Preparation didn't complete; delete the instance directory.
			// Use os.RemoveAll because store.Inspect may fail if lima.yaml is missing.
			instanceDir := filepath.Join(instance.LimaHome(), limaVM.Name)
			logger.Info("Deleting incomplete instance directory", "path", instanceDir)
			if err := os.RemoveAll(instanceDir); err != nil {
				logger.Error(err, "Failed to delete incomplete instance directory")
				return ctrl.Result{}, err
			}
		}
		// Requeue to continue with fresh state after cleanup.
		return ctrl.Result{Requeue: true}, nil
	}

	// Delete any leftover instance from a failed deletion before setting up owner references.
	if limaVM.Status.TemplateConfigMap == "" {
		existingInst, err := store.Inspect(ctx, limaVM.Name)
		if err == nil && existingInst != nil {
			logger.Info("Deleting leftover Lima instance", "instance", limaVM.Name)
			if err := limainstance.Delete(ctx, existingInst, true); err != nil {
				logger.Error(err, "Failed to delete leftover Lima instance")
				return ctrl.Result{}, err
			}
		}
	}

	// Get the template ConfigMap (created by the mutating webhook)
	configMapName := limaVM.GetTemplateConfigMapName()
	templateConfigMap := &corev1.ConfigMap{}
	configMapKey := types.NamespacedName{Name: configMapName, Namespace: limaVM.Namespace}
	if err := r.Get(ctx, configMapKey, templateConfigMap); err != nil {
		logger.Error(err, "Failed to get template ConfigMap")
		return ctrl.Result{}, err
	}

	// Set owner reference (the webhook already set the finalizer)
	if !base.IsOwnedByUID(templateConfigMap, limaVM.GetUID()) {
		if err := ctrl.SetControllerReference(&limaVM, templateConfigMap, r.Scheme); err != nil {
			logger.Error(err, "Failed to set owner reference on template ConfigMap")
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, templateConfigMap); err != nil {
			logger.Error(err, "Failed to update template ConfigMap")
			return ctrl.Result{}, err
		}
		logger.Info("Set owner reference on template ConfigMap", "ConfigMap.Name", configMapName)
		return ctrl.Result{}, nil
	}

	// Record the template ConfigMap name in status
	if limaVM.Status.TemplateConfigMap == "" {
		limaVM.Status.TemplateConfigMap = configMapName
		if err := r.Status().Update(ctx, &limaVM); err != nil {
			logger.Error(err, "Failed to update status.templateConfigMap")
			return ctrl.Result{}, err
		}
		logger.Info("Set status.templateConfigMap", "ConfigMap.Name", configMapName)
		return ctrl.Result{}, nil
	}

	// Instance already created - proceed to handle running state
	if apimeta.IsStatusConditionTrue(limaVM.Status.Conditions, ConditionCreated) {
		return r.handleRunningState(ctx, &limaVM)
	}

	// Instance exists on disk (perhaps from a previous reconcile); record the condition
	// and return to let the next reconcile handle running state (one mutation per reconcile).
	existingInst, err := store.Inspect(ctx, limaVM.Name)
	if err == nil && existingInst != nil {
		logger.Info("Lima instance already exists", "instance", limaVM.Name)
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance exists")
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status for existing instance")
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// Extract template data
	templateData, ok := templateConfigMap.Data[v1alpha1.TemplateConfigMapKey]
	if !ok || templateData == "" {
		err := errors.New("template ConfigMap missing template data")
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Create the Lima instance
	inst, err := limainstance.Create(ctx, limaVM.Name, []byte(templateData), false)
	if err != nil {
		logger.Error(err, "Failed to create Lima instance")
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after instance creation failure")
		}
		return ctrl.Result{}, err
	}

	// Create sentinel file to mark preparation in progress.
	if err := createSentinel(limaVM.Name); err != nil {
		logger.Error(err, "Failed to create sentinel file")
		if delErr := limainstance.Delete(ctx, inst, true); delErr != nil {
			logger.Error(delErr, "Failed to clean up instance after sentinel creation failure")
		}
		return ctrl.Result{}, err
	}

	// Prepare the instance: download images and create disks.
	// The guestAgent path is a placeholder during Prepare (only stored for later);
	// limactl start will look up the real path when called.
	if _, err := limainstance.Prepare(ctx, inst, "placeholder"); err != nil {
		logger.Error(err, "Failed to prepare Lima instance")
		// Clean up the partially created instance so the next reconcile doesn't
		// see it as existing and skip creation.
		if delErr := limainstance.Delete(ctx, inst, true); delErr != nil {
			logger.Error(delErr, "Failed to clean up instance after prepare failure")
		}
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after instance preparation failure")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Created Lima instance", "instance", limaVM.Name)
	r.setCondition(&limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance created successfully")
	if err := r.Status().Update(ctx, &limaVM); err != nil {
		logger.Error(err, "Failed to update status after instance creation")
		return ctrl.Result{}, err
	}

	// Remove sentinel file now that preparation is complete and status is updated.
	if err := removeSentinel(limaVM.Name); err != nil {
		logger.Error(err, "Failed to remove sentinel file after successful creation")
		return ctrl.Result{}, err
	}

	// Return and let the next reconcile handle running state (one mutation per reconcile)
	return ctrl.Result{}, nil
}

// setCondition updates or adds a condition in the LimaVM status.
func (r *LimaVMReconciler) setCondition(limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) {
	changed := apimeta.SetStatusCondition(&limaVM.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
	if changed && r.Recorder != nil {
		r.Recorder.Eventf(limaVM, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
	}
}

// updateCondition sets a condition and updates the status in one call.
func (r *LimaVMReconciler) updateCondition(ctx context.Context, limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	r.setCondition(limaVM, conditionType, status, reason, message)
	return r.Status().Update(ctx, limaVM)
}

// SetupWithManager sets up the controller with the Manager.
func (r *LimaVMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LimaVM{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
