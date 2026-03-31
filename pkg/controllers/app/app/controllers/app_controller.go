// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	appName                        = "app"
	limaVMName, inputConfigMapName = "rd", "rd"

	// requeueAfterDeletion is how long to wait between checks while the LimaVM
	// controller is running its teardown (stopping the VM, removing disk files).
	requeueAfterDeletion = 2 * time.Second
)

// AppReconciler reconciles the singleton App resource and manages its LimaVM lifecycle.
type AppReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	LimaTemplateData string
}

// Reconcile implements a singleton app reconciliation loop.
// The app controller is a cluster-scoped singleton - only one instance named 'app' is allowed.
func (r *AppReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app v1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Unable to fetch App singleton")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion, delete the LimaVM and wait for it to finish cleaning up.
	if base.IsBeingDeleted(&app) {
		log.Info("App resource is being deleted, performing cleanup")

		namespace := app.GetResourceNamespace()
		limaVM := &limav1alpha1.LimaVM{}
		err := r.Get(ctx, client.ObjectKey{Name: limaVMName, Namespace: namespace}, limaVM)
		switch {
		case apierrors.IsNotFound(err):
			// LimaVM is gone. Clean up any ConfigMaps that may have been left behind.
			inputCM := &corev1.ConfigMap{}
			if cmErr := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM); cmErr == nil {
				if cmErr := r.Delete(ctx, inputCM); cmErr != nil && !apierrors.IsNotFound(cmErr) {
					return ctrl.Result{}, fmt.Errorf("failed to delete input ConfigMap during cleanup: %w", cmErr)
				}
			} else if !apierrors.IsNotFound(cmErr) {
				return ctrl.Result{}, fmt.Errorf("failed to fetch input ConfigMap during cleanup: %w", cmErr)
			}
			return ctrl.Result{}, base.RemoveCleanupFinalizer(ctx, r.Client, &app)
		case err != nil:
			return ctrl.Result{}, err
		default:
			if err := base.RemoveOwnedFinalizer(ctx, r.Client, limaVM, v1alpha1.AppKind); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove owned finalizer from LimaVM: %w", err)
			}
			if !base.IsBeingDeleted(limaVM) {
				if err := r.Delete(ctx, limaVM); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to delete LimaVM: %w", err)
				}
				log.Info("Requested LimaVM deletion, waiting for teardown")
			} else {
				log.Info("Waiting for LimaVM deletion to complete")
			}
			return ctrl.Result{RequeueAfter: requeueAfterDeletion}, nil
		}
	}

	// Make sure the App is finalized so deletion goes through cleanup.
	if added, err := base.EnsureCleanupFinalizer(ctx, r.Client, &app); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{}, nil
	}

	namespace := app.GetResourceNamespace()

	// Check whether the LimaVM already exists. If not, create the input ConfigMap and LimaVM.
	limaVM := &limav1alpha1.LimaVM{}
	limaVMErr := r.Get(ctx, client.ObjectKey{Name: limaVMName, Namespace: namespace}, limaVM)
	if limaVMErr != nil && !apierrors.IsNotFound(limaVMErr) {
		return ctrl.Result{}, limaVMErr
	}

	if apierrors.IsNotFound(limaVMErr) {
		inputCM := &corev1.ConfigMap{}
		err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if apierrors.IsNotFound(err) {
			inputCM = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      inputConfigMapName,
					Namespace: namespace,
				},
				Data: map[string]string{
					limav1alpha1.TemplateConfigMapKey: r.LimaTemplateData,
				},
			}
			if err := ctrl.SetControllerReference(&app, inputCM, r.Scheme); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set owner reference on input ConfigMap: %w", err)
			}
			if err := r.Create(ctx, inputCM); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
			}
		}

		limaVM = &limav1alpha1.LimaVM{
			ObjectMeta: metav1.ObjectMeta{
				Name:       limaVMName,
				Namespace:  namespace,
				Finalizers: []string{base.OwnedFinalizerFor(v1alpha1.AppKind)},
			},
			Spec: limav1alpha1.LimaVMSpec{
				TemplateRef: limav1alpha1.TemplateReference{
					Name:      inputConfigMapName,
					Namespace: namespace,
				},
				Running: app.Spec.Running,
			},
		}
		if err := ctrl.SetControllerReference(&app, limaVM, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set owner reference on LimaVM: %w", err)
		}
		if err := r.Create(ctx, limaVM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create LimaVM: %w", err)
		}
		log.Info("Created LimaVM", "name", limaVMName, "namespace", namespace)
		return ctrl.Result{}, nil
	}

	// LimaVM exists — clean up the input ConfigMap if it still lingers from the
	// creation phase and return to requeueing on LimaVM updates.
	inputConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputConfigMap); err == nil {
		if err := r.Delete(ctx, inputConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete input ConfigMap: %w", err)
		}
		log.Info("Deleted input ConfigMap after LimaVM created its own copy")
		// ConfigMaps are not watched (no Owns(&corev1.ConfigMap{})), so deleting
		// one produces no watch event. Requeue explicitly to guarantee the next
		// reconcile runs rather than relying on implicit LimaVM status activity.
		return ctrl.Result{Requeue: true}, nil
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to fetch input ConfigMap: %w", err)
	}

	// Propagate spec.running from App into the LimaVM.
	if limaVM.Spec.Running != app.Spec.Running {
		limaVM.Spec.Running = app.Spec.Running
		if err := r.Update(ctx, limaVM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to propagate running state to LimaVM: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Mirror LimaVM status conditions onto the App status.
	// The priority chain above returns after every other action, so the App's
	// resourceVersion from the initial Get is still current — no re-read needed.
	// Truncate messages defensively: the LimaVM controller already truncates at
	// source, but guarding here ensures a future bypass can't cause the App
	// status update to fail CRD validation.
	statusChanged := false
	for _, cond := range limaVM.Status.Conditions {
		statusChanged = apimeta.SetStatusCondition(&app.Status.Conditions, metav1.Condition{
			Type:               cond.Type,
			Status:             cond.Status,
			Reason:             cond.Reason,
			Message:            base.TruncateConditionMessage(cond.Message),
			ObservedGeneration: app.Generation,
			LastTransitionTime: cond.LastTransitionTime,
		}) || statusChanged
	}
	if statusChanged {
		if err := r.Status().Update(ctx, &app); err != nil {
			log.Error(err, "Unable to update App status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.App{}).
		Owns(&limav1alpha1.LimaVM{}).
		Complete(r)
}
