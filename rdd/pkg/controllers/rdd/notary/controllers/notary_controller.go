// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Notary reconciler, which watches a
// ConfigMap key and records each value change into a numbered ConfigMap.
package controllers

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// NotaryReconciler reconciles a Notary object.
type NotaryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Manager  ctrl.Manager
}

// +kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=notaries,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=notaries/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=notaries/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The Notary controller tracks changes to a spec field and records them in a ConfigMap.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *NotaryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Notary instance
	var notary v1alpha1.Notary
	if err := r.Get(ctx, req.NamespacedName, &notary); err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return without error
			log.Info("Notary resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request
		log.Error(err, "Failed to get Notary")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if base.IsBeingDeleted(&notary) {
		return r.handleDeletion(ctx, &notary)
	}

	// Add finalizer if not present
	if added, err := base.EnsureCleanupFinalizer(ctx, r.Client, &notary); err != nil {
		log.Error(err, "Failed to add finalizer")
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{}, nil
	}

	// Record event for every reconcile with current spec value
	r.Recorder.Eventf(&notary, nil, corev1.EventTypeNormal, "Reconcile", "SpecUpdate", "Notary spec updated with value: %s", notary.Spec.Value)

	// Check if the value has changed
	oldValue := notary.Status.LastRecordedValue
	if oldValue != notary.Spec.Value {
		log.Info("Value changed, recording in ConfigMap",
			"oldValue", oldValue,
			"newValue", notary.Spec.Value)

		// Record the change in the ConfigMap
		if err := r.recordValueChange(ctx, &notary); err != nil {
			log.Error(err, "Failed to record value change")
			r.Recorder.Eventf(&notary, nil, corev1.EventTypeWarning, "ConfigMapError", "SyncValue", "Failed to record value change: %v", err)
			return ctrl.Result{}, err
		}

		// Update the status
		notary.Status.LastRecordedValue = notary.Spec.Value
		notary.Status.ChangeCount++
		notary.Status.ConfigMapStatus = "Updated"

		if err := r.Status().Update(ctx, &notary); err != nil {
			log.Error(err, "Failed to update Notary status")
			r.Recorder.Eventf(&notary, nil, corev1.EventTypeWarning, "StatusUpdateError", "SyncValue", "Failed to update status: %v", err)
			return ctrl.Result{}, err
		}

		r.Recorder.Eventf(&notary, nil, corev1.EventTypeNormal, "ValueRecorded", "SyncValue", "Value change recorded in ConfigMap: %s -> %s", oldValue, notary.Spec.Value)
	} else {
		r.Recorder.Eventf(&notary, nil, corev1.EventTypeNormal, "NoChange", "SyncValue", "Spec update received but value unchanged, no ConfigMap update needed")
	}

	return ctrl.Result{}, nil
}

// handleDeletion handles cleanup when a Notary is being deleted.
// It removes all owned ConfigMaps and then removes the finalizer to allow
// the Notary to be deleted.
func (r *NotaryReconciler) handleDeletion(ctx context.Context, notary *v1alpha1.Notary) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Clean up all owned ConfigMaps using the helper
	if err := base.DeleteOwnedResources(ctx, r.Client, notary, r.Manager); err != nil {
		log.Error(err, "Failed to delete owned resources")
		return ctrl.Result{}, err
	}

	// Remove finalizer to allow deletion
	if err := base.RemoveCleanupFinalizer(ctx, r.Client, notary); err != nil {
		log.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	log.Info("Successfully cleaned up ConfigMaps and removed finalizer")
	return ctrl.Result{}, nil
}

// recordValueChange records the value change in the specified ConfigMap.
func (r *NotaryReconciler) recordValueChange(ctx context.Context, notary *v1alpha1.Notary) error {
	log := logf.FromContext(ctx)

	// Get or create the ConfigMap
	configMap := &corev1.ConfigMap{}
	configMapName := notary.Spec.ConfigMapName
	configMapKey := client.ObjectKey{
		Name:      configMapName,
		Namespace: notary.Namespace,
	}

	err := r.Get(ctx, configMapKey, configMap)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create a new ConfigMap
			configMap = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: notary.Namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "notary-controller",
						"app.kubernetes.io/instance":   notary.Name,
					},
				},
				Data: make(map[string]string),
			}

			// Set the Notary as the owner
			if err := controllerutil.SetControllerReference(notary, configMap, r.Scheme); err != nil {
				return err
			}

			log.Info("Creating new ConfigMap", "configMapName", configMapName)
		} else {
			return err
		}
	}

	// Add the new entry to the ConfigMap
	timestamp := time.Now().Format(time.RFC3339)
	entryKey := fmt.Sprintf("change_%03d", notary.Status.ChangeCount)
	entryValue := fmt.Sprintf("timestamp=%s,value=%s", timestamp, notary.Spec.Value)

	if configMap.Data == nil {
		configMap.Data = make(map[string]string)
	}
	configMap.Data[entryKey] = entryValue

	// Also store a summary
	configMap.Data["latest_change"] = entryValue
	configMap.Data["change_count"] = strconv.Itoa(notary.Status.ChangeCount)

	// Create or update the ConfigMap
	if configMap.ResourceVersion == "" {
		return r.Create(ctx, configMap)
	}
	return r.Update(ctx, configMap)
}

// SetupWithManager sets up the controller with the Manager.
func (r *NotaryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Notary{}).
		Complete(r)
}
