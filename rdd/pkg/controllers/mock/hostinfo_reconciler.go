// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	rddv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
)

type hostInfoReconciler struct {
	client.Client
}

const hostInfoName = "system"

func (r *hostInfoReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	// For the mock controller, we just always create a hostinfo resource whenever
	// we get triggered.

	hi := rddv1alpha1.HostInfo{
		ObjectMeta: metav1.ObjectMeta{
			Name: hostInfoName,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r, &hi, nil); err != nil {
		return ctrl.Result{}, err
	}
	hi.Status.CPUs = 32
	hi.Status.Memory = 12 * 1024 * 1024 * 1024
	if err := r.Status().Update(ctx, &hi); err != nil {
		logger.Error(err, "Failed to update HostInfo status", "name", hi.Name)
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled host-info", "name", hi.Name)
	return ctrl.Result{}, nil
}

func (r *hostInfoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("hostinfo_reconciler").
		// We get triggered on App changes.
		For(&appv1alpha1.App{}).
		// We also watch hostinfo changes, to revert them as needed.
		Watches(&rddv1alpha1.HostInfo{}, handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []ctrl.Request {
			return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: "unused"}}} // We never actually look at the request.
		})).
		Complete(r)
}
