// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// DemoReconciler reconciles a Demo object.
type DemoReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=app.rancherdesktop.io,resources=demos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.rancherdesktop.io,resources=demos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.rancherdesktop.io,resources=demos/finalizers,verbs=update

// Reconcile implements a singleton demo reconciliation loop.
// The demo controller is a cluster-scoped singleton - only one instance named 'demo' is allowed.
func (r *DemoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Enforce singleton pattern - only allow resource named 'demo'
	if req.Name != "demo" {
		log.Info("Rejecting Demo resource with invalid name - only 'demo' is allowed", "name", req.Name)
		// For cluster-scoped resources, we don't need to handle namespace
		var demo appv1alpha1.Demo
		if err := r.Get(ctx, client.ObjectKey{Name: req.Name}, &demo); err == nil {
			// Delete invalid instances
			if err := r.Delete(ctx, &demo); err != nil {
				log.Error(err, "Failed to delete invalid Demo instance", "name", req.Name)
			} else {
				log.Info("Deleted invalid Demo instance", "name", req.Name)
			}
		}
		return ctrl.Result{}, nil
	}

	// Fetch the singleton Demo instance
	var demo appv1alpha1.Demo
	if err := r.Get(ctx, client.ObjectKey{Name: "demo"}, &demo); err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Unable to fetch Demo singleton")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion - Demo controller doesn't create child resources or use finalizers
	if base.IsBeingDeleted(&demo) {
		log.Info("Demo deletion handled successfully")
		return ctrl.Result{}, nil
	}

	// Simple demo logic: increment processed count if it's less than desired count
	if demo.Status.ProcessedCount < demo.Spec.Count {
		demo.Status.ProcessedCount++
		demo.Status.LastProcessed = time.Now().Format(time.RFC3339)

		// Update conditions for processing state
		r.setCondition(&demo, appv1alpha1.DemoConditionReady, metav1.ConditionTrue, "Processing", "Demo controller is ready and processing messages")
		r.setCondition(&demo, appv1alpha1.DemoConditionProcessing, metav1.ConditionTrue, "InProgress", "Processing demo messages")
		r.setCondition(&demo, appv1alpha1.DemoConditionCompleted, metav1.ConditionFalse, "InProgress", "Processing is still in progress")

		log.Info("Processing demo message", "message", demo.Spec.Message, "count", demo.Status.ProcessedCount)
		r.Recorder.Eventf(&demo, "Normal", "Processing", "Processed message %d of %d: %s", demo.Status.ProcessedCount, demo.Spec.Count, demo.Spec.Message)
	} else {
		// Update conditions for completed state
		r.setCondition(&demo, appv1alpha1.DemoConditionReady, metav1.ConditionTrue, "Ready", "Demo controller is ready")
		r.setCondition(&demo, appv1alpha1.DemoConditionProcessing, metav1.ConditionFalse, "Completed", "Processing has completed")
		r.setCondition(&demo, appv1alpha1.DemoConditionCompleted, metav1.ConditionTrue, "Completed", "All messages have been processed successfully")

		r.Recorder.Event(&demo, "Normal", "Completed", "Demo processing completed successfully")
	}

	// Update status
	if err := r.Status().Update(ctx, &demo); err != nil {
		log.Error(err, "Unable to update Demo status")
		return ctrl.Result{}, err
	}

	// Requeue if we haven't reached the desired count
	if demo.Status.ProcessedCount < demo.Spec.Count {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	return ctrl.Result{}, nil
}

// setCondition sets or updates a condition in the demo status.
func (r *DemoReconciler) setCondition(demo *appv1alpha1.Demo, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now())

	// Find existing condition of this type
	for i, condition := range demo.Status.Conditions {
		if condition.Type != conditionType {
			continue
		}
		// Update existing condition if parameters changed.
		changed := false
		if condition.Status != status {
			demo.Status.Conditions[i].Status = status
			demo.Status.Conditions[i].LastTransitionTime = now
			changed = true
		}
		if condition.Reason != reason || condition.Message != message {
			demo.Status.Conditions[i].Reason = reason
			demo.Status.Conditions[i].Message = message
			changed = true
		}
		if changed {
			r.Recorder.Eventf(demo, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
		}
		return
	}

	// Add new condition
	demo.Status.Conditions = append(demo.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	r.Recorder.Eventf(demo, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DemoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1alpha1.Demo{}).
		Complete(r)
}
