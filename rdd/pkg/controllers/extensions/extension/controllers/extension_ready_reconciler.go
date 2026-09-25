// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Extension reconcilers and validating
// webhook. Reconciliation is split per status condition (e.g.
// ExtensionReadyReconciler for Ready), mirroring the App resource's
// per-condition reconcilers (AppReconciler, EngineReconciler,
// KubernetesReconciler, etc.) elsewhere in this codebase.
package controllers

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// ExtensionReadyReconciler reconciles an Extension object's Ready condition.
type ExtensionReadyReconciler struct {
	client.Client
}

var _ reconcile.ObjectReconciler[*v1alpha1.Extension] = &ExtensionReadyReconciler{}

// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions/status,verbs=get;update;patch

// Reconcile the Extension resource's Ready condition.
func (r *ExtensionReadyReconciler) Reconcile(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Extension Ready condition",
		"name", ext.Name, "namespace", ext.Namespace)

	key := client.ObjectKeyFromObject(ext)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return err
		}

		installed := apimeta.FindStatusCondition(latest.Status.Conditions, v1alpha1.ExtensionConditionInstalled)
		started := apimeta.FindStatusCondition(latest.Status.Conditions, v1alpha1.ExtensionConditionStarted)
		status, reason, message := readyConditionFor(installed, started)

		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ExtensionConditionReady,
			Status:             status,
			ObservedGeneration: latest.Generation,
			Reason:             reason,
			Message:            message,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	}); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return ctrl.Result{}, nil
}

// isInstalledTerminalFailure reports whether reason is one of the terminal
// (non-retryable without user interaction) failure reasons for the Installed
// condition.
func isInstalledTerminalFailure(reason string) bool {
	switch reason {
	case v1alpha1.ExtensionInstalledReasonResolveFailed,
		v1alpha1.ExtensionInstalledReasonDownloadFailed,
		v1alpha1.ExtensionInstalledReasonExtractFailed,
		v1alpha1.ExtensionInstalledReasonPostInstallFailed,
		v1alpha1.ExtensionInstalledReasonDeleteFailed:
		return true
	default:
		return false
	}
}

// readyConditionFor derives the desired status, reason, and message for the
// Ready condition from the current Installed and Started conditions only.
// Ready is intentionally kept as a pure function of those two conditions so
// it stays simple to test and to reason about as the Installed/Started
// conditions gain real producers.
func readyConditionFor(installed, started *metav1.Condition) (status metav1.ConditionStatus, reason, message string) {
	if installed == nil {
		// Nothing has started installing the extension yet.
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonCreated,
			"Install has not started yet"
	}

	// startedReason defaults to "" when Started has not been set yet, which
	// is the common case today since nothing produces it.
	var startedReason string
	if started != nil {
		startedReason = started.Reason
	}

	switch {
	case isInstalledTerminalFailure(installed.Reason):
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonBroken,
			"Extension installation failed; user interaction required"

	case startedReason == v1alpha1.ExtensionStartedReasonStartFailed:
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonBroken,
			"Extension failed to start; user interaction required"

	case installed.Reason == v1alpha1.ExtensionInstalledReasonPreUninstallRunning,
		installed.Reason == v1alpha1.ExtensionInstalledReasonDeleting,
		installed.Reason == v1alpha1.ExtensionInstalledReasonUninstalled:
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonUninstalling,
			"Extension is being removed"

	case installed.Status != metav1.ConditionTrue:
		// Installed has not yet reached its successful terminal state
		// (Downloading, Extracting, PostInstallRunning, or simply not True).
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonInstalling,
			"Extension is being installed"

	case startedReason == v1alpha1.ExtensionStartedReasonStarted:
		return metav1.ConditionTrue, v1alpha1.ExtensionReadyReasonReady,
			"Extension is running and ready"

	case startedReason == v1alpha1.ExtensionStartedReasonStopping:
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonStopping,
			"Extension is being stopped"

	default:
		// Installed, but Started is missing, or Installing/Starting.
		return metav1.ConditionFalse, v1alpha1.ExtensionReadyReasonStarting,
			"Extension is being started"
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExtensionReadyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Extension{}).
		Named("extension-ready-reconciler").
		Complete(reconcile.AsReconciler(mgr.GetClient(), r))
}
