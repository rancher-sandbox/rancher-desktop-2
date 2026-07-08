// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"sync"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

const k3sVersionsConfigMapName = "k3s-versions"

// K3sVersionsReconciler reconciles the internal k3s versions config map; this
// is used by both the app reconciler and the front end to determine which k3s
// versions are available for use.
type K3sVersionsReconciler struct {
	client.Client
}

// DesiredLabels is the desired labels for the k3s-versions config map.
// This is exported so the webhook can use it to filter for the config map.
var DesiredLabels = sync.OnceValue(func() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "rdd-k3s-versions",
		"app.kubernetes.io/component":  "k3s-versions",
		"app.kubernetes.io/managed-by": "rancher-desktop-daemon",
	}
})

// DesiredLabels returns a copy of the desired labels for the k3s-versions
// config map.  This is exported so the controller can use the labels to filter
// the config maps that get watched.
func DesiredLabels() map[string]string {
	return maps.Clone(desiredLabels)
}

// Reconcile implements [reconcile.Reconciler].
func (r *K3sVersionsReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	cm := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: req.Namespace,
			Name:      req.Name,
		},
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r, &cm, func() error {
		if cm.Labels == nil {
			cm.Labels = make(map[string]string)
		}
		for k, v := range DesiredLabels() {
			cm.Labels[k] = v
		}
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		desiredData, err := k3sVersionData()
		if err != nil {
			return err
		}
		for k, v := range desiredData {
			cm.Data[k] = v
		}
		return nil
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to create or update k3s versions config map: %w", err)
	}

	logger.V(1).Info("reconciled k3s-versions config map", "result", result)
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the given Manager.
func (r *K3sVersionsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger whenever an app gets created; this ensures that the config map
	// exists.
	return builder.ControllerManagedBy(mgr).
		// We manage config maps, of a particular namespace and name.
		For(&v1.ConfigMap{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return o.GetNamespace() == controllers.RDDSystemNamespace && o.GetName() == k3sVersionsConfigMapName
		}))).
		// We also enqueue a reconcile request when an app is created.
		Watches(&v1alpha1.App{},
			// Enqueue a reconcile request for the relevant config map on app change...
			handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
				return []reconcile.Request{{
					NamespacedName: client.ObjectKey{
						Namespace: controllers.RDDSystemNamespace,
						Name:      k3sVersionsConfigMapName,
					},
				}}
			}),
			// ...but only on app creation, since we only need to make sure the config map exists.
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(event.CreateEvent) bool {
					return true
				},
				UpdateFunc: func(event.UpdateEvent) bool {
					return false
				},
				DeleteFunc: func(event.DeleteEvent) bool {
					return false
				},
				GenericFunc: func(event.GenericEvent) bool {
					return false
				},
			}),
		).
		Complete(r)
}
