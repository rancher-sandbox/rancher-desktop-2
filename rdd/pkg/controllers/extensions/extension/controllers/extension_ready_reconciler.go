// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Extension reconcilers and validating
// webhook. Reconciliation is split per status condition (e.g.
// Ready condition), mirroring the App resource's
// per-condition reconcilers (AppReconciler, EngineReconciler,
// KubernetesReconciler, etc.) elsewhere in this codebase.
package controllers

import (
	"context"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// reconcileReady reconciles the Ready condition for the Extension.
func (r *ExtensionReconciler) reconcileReady(ctx context.Context, ext *v1alpha1.Extension) error {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Extension Ready condition",
		"name", ext.Name, "namespace", ext.Namespace)

	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
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
		if err := r.Status().Update(ctx, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		// On success, start a new reconcile.
		return requeueError(0)
	})
}

// isInstalledTerminalFailure reports whether reason is one of the terminal
// (non-retryable without user interaction) failure reasons for the Installed
// condition.
func isInstalledTerminalFailure(reason string) bool {
	return reason == v1alpha1.ExtensionInstalledReasonFailed
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
