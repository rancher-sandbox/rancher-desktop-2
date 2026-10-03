// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// upRequestReconciler implements the ComposeUpRequest reconcile loop.
type upRequestReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests/finalizers,verbs=update

// Reconcile implements [reconcile.Reconciler].
func (r *upRequestReconciler) Reconcile(_ context.Context, _ ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *upRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("compose-up-request-reconciler").
		For(&v1alpha1.ComposeUpRequest{}).
		Complete(r)
}

var _ reconcile.Reconciler = &upRequestReconciler{}
