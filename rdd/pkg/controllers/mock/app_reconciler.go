// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// type appReconciler reconiles the mock App resource, resulting in the app
// always being Settled.
type appReconciler struct {
	client.Client
	Recorder events.EventRecorder
}

func (r *appReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check for the CRD to be registered.
	const crdName = "apps.app.rancherdesktop.io"
	var crd apiextensionsv1.CustomResourceDefinition
	if err := r.Client.Get(ctx, types.NamespacedName{Name: crdName}, &crd); err != nil {
		log.Error(err, "Failed to get CRD", "crd", crdName)
		return ctrl.Result{}, err
	}

	// Get the resource to reconcile.
	var app v1alpha1.App
	if err := r.Client.Get(ctx, req.NamespacedName, &app); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get App resource", "name", req.NamespacedName)
		return ctrl.Result{}, err
	}

	// Ensure the `Settled` condition matches the running state.
	status := metav1.ConditionFalse
	if app.Spec.Running {
		status = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
		Type:    "Settled",
		Status:  status,
		Reason:  "MockController",
		Message: fmt.Sprintf("app.running set to %v", app.Spec.Running),
	})
	if err := r.Status().Update(ctx, &app); err != nil {
		log.Error(err, "Failed to update App status", "name", req.NamespacedName)
		return ctrl.Result{}, err
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
