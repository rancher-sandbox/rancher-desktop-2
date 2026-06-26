// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// appReconciler reconciles the mock App resource, resulting in the app's
// Settled condition matching the app spec's running field.
type appReconciler struct {
	client.Client
}

func (r *appReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Get the resource to reconcile.
	var app v1alpha1.App
	if err := r.Client.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get App resource", "name", req.NamespacedName)
		return ctrl.Result{}, err
	}

	if !app.GetDeletionTimestamp().IsZero() {
		// The app is being deleted; nothing to do here yet.
		return ctrl.Result{}, nil
	}

	// Ensure the `Settled` condition matches the running state.
	status := metav1.ConditionFalse
	if app.Spec.Running {
		status = metav1.ConditionTrue
	}
	changed := apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:               v1alpha1.AppConditionSettled,
		Status:             status,
		ObservedGeneration: app.Generation,
		Reason:             "MockController",
		Message:            fmt.Sprintf("app.running set to %v", app.Spec.Running),
	})
	if changed {
		if err := r.Status().Update(ctx, &app); err != nil {
			log.Error(err, "Failed to update App status", "name", req.NamespacedName)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *appReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.App{}).
		Complete(r)
	if err != nil {
		return err
	}

	return nil
}
