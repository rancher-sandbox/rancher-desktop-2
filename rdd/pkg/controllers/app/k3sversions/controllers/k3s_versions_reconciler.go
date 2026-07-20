// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"maps"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const k3sVersionsConfigMapName = "k3s-versions"

// K3sVersionsReconciler reconciles on App resource changes to create or delete
// the k3s-versions config map; that config map is used by the front end to
// display supported Kubernetes versions in the preferences dialog.
type K3sVersionsReconciler struct {
	client.Client
}

// desiredLabels is the desired labels for the k3s-versions config map.
// The users should not modify this structure; using `maps.Clone` is recommended.
var desiredLabels = map[string]string{
	"app.kubernetes.io/name":       "rdd-k3s-versions",
	"app.kubernetes.io/component":  "k3s-versions",
	"app.kubernetes.io/managed-by": "rdd-k3s-versions",
}

// DesiredLabels returns a copy of the desired labels for the k3s-versions
// config map.  This is exported so the controller can use the labels to filter
// the config maps that get watched.
func DesiredLabels() map[string]string {
	return maps.Clone(desiredLabels)
}

// Reconcile implements [reconcile.Reconciler].
func (r *K3sVersionsReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	app := v1alpha1.App{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: req.Name}, &app); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return reconcile.Result{}, fmt.Errorf("failed to get app %s/%s: %w", req.Namespace, req.Name, err)
		}
		logger.V(1).Info("app not found; it may have been deleted", "namespace", req.Namespace, "name", req.Name)
		// The k3s versions config map is owned by the app, and the app reconciler
		// is responsible for deleting its owned resources.  Without the app, we do
		// not know which namespace in which to look for the config map anyway, so
		// there is nothing we can do here.
		return reconcile.Result{}, nil
	}

	if app.Spec.Namespace == "" {
		// The app doesn't have a namespace yet; the app reconciler should deal with
		// it before we create the config map.
		logger.V(1).Info("app does not have a namespace yet", "namespace", req.Namespace, "name", req.Name)
		return reconcile.Result{}, nil
	}

	cm := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: app.Spec.Namespace,
			Name:      k3sVersionsConfigMapName,
		},
	}

	if base.IsBeingDeleted(&app) {
		// The app is being deleted; the app's reconciler will delete all the
		// objects it owns, which includes the k3s-versions config map.
		return reconcile.Result{}, nil
	}

	result, err := controllerutil.CreateOrUpdate(ctx, r, &cm, func() error {
		if cm.Labels == nil {
			cm.Labels = make(map[string]string)
		}
		maps.Copy(cm.Labels, desiredLabels)
		cm.Labels["app.kubernetes.io/instance"] = app.Name
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		desiredData, err := k3sVersionData()
		if err != nil {
			return err
		}
		maps.Copy(cm.Data, desiredData)
		if err := ctrl.SetControllerReference(&app, &cm, r.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference for k3s versions config map: %w", err)
		}

		return nil
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to create or update k3s versions config map: %w", err)
	}

	logger.V(1).Info("reconciled k3s-versions config map", "result", result, "namespace", cm.Namespace)
	return reconcile.Result{}, nil
}

// SetupWithManager sets up the controller with the given Manager.
func (r *K3sVersionsReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Trigger whenever an app gets created; this ensures that the config map
	// exists.
	return builder.ControllerManagedBy(mgr).
		Named("k3s_versions_reconciler").
		// We get triggered on App changes.
		For(&v1alpha1.App{}).
		// We also watch for the config map being changed, so we can reset it as
		// needed.  Filter to only our config map.
		Owns(&v1.ConfigMap{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetName() == k3sVersionsConfigMapName
		}))).
		Complete(r)
}
