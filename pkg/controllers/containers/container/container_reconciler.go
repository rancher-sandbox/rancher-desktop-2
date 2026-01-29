// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package container

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// ContainerReconciler reconciles a Container object.
type ContainerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=containers/finalizers,verbs=update

// Reconcile implements a container reconciliation loop.
func (r *ContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var container containersv1alpha1.Container
	if err := r.Get(ctx, req.NamespacedName, &container); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Container not found")
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch Container")
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&container) {
		return ctrl.Result{}, nil
	}

	convertStatusToCondition := func(status, conditionType, reason string) {
		if string(container.Status.Status) == status {
			r.setCondition(&container, conditionType, metav1.ConditionTrue, reason, "Container is "+status)
		} else {
			r.setCondition(&container, conditionType, metav1.ConditionFalse, "Not"+reason, "Container is not "+status)
		}
	}

	convertStatusToCondition("running", "Running", "Running")
	convertStatusToCondition("paused", "Paused", "Paused")
	convertStatusToCondition("restarting", "Restarting", "Restarting")
	convertStatusToCondition("dead", "Dead", "Dead")
	// TODO: Figure out how to derive OOMKilled
	r.setCondition(&container, "OOMKilled", metav1.ConditionUnknown, "OOMKilled", "Unable to tell if container is OOMKilled")

	// Set defaults
	if container.Status.Status == "" {
		container.Status.Status = containersv1alpha1.ContainerStatusUnknown
	}

	if err := r.Status().Update(ctx, &container); err != nil {
		log.Error(err, "unable to update Container status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// setCondition sets or updates a condition in the container status.
func (r *ContainerReconciler) setCondition(container *containersv1alpha1.Container, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now())

	// Find existing condition of this type
	for i, condition := range container.Status.Conditions {
		if condition.Type != conditionType {
			continue
		}
		// Update existing condition if parameters changed.
		changed := false
		if condition.Status != status {
			container.Status.Conditions[i].Status = status
			container.Status.Conditions[i].LastTransitionTime = now
			changed = true
		}
		if condition.Reason != reason || condition.Message != message {
			container.Status.Conditions[i].Reason = reason
			container.Status.Conditions[i].Message = message
			changed = true
		}
		if changed {
			r.Recorder.Eventf(container, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
		}
		return
	}

	// Add new condition
	container.Status.Conditions = append(container.Status.Conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	})
	r.Recorder.Eventf(container, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&containersv1alpha1.Container{}).
		Complete(r)
}
