// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// extractedFinalizer is the finalizer used to ensure that any resources
// associated with an extension extraction is cleaned up.
const extractedFinalizer = "extensions.rancherdesktop.io/extracted"

// reconcileExtracted reconciles the Extracted condition for the Extension.
func (r *ExtensionReconciler) reconcileExtracted(ctx context.Context, ext *v1alpha1.Extension) error {
	log := logf.FromContext(ctx)
	log.V(1).Info("Reconciling Extension Extracted condition",
		"name", ext.Name, "namespace", ext.Namespace)

	if base.IsBeingDeleted(ext) {
		r.extractor.abort(ext.GetUID())
		return base.RemoveFinalizerWithRetry(ctx, r.Client, ext, extractedFinalizer)
	}

	if !controllerutil.ContainsFinalizer(ext, extractedFinalizer) {
		return base.AddFinalizerWithRetry(ctx, r.extractor, ext, extractedFinalizer)
	}

	condition := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionExtracted)
	r.engineMu.Lock()
	engine := r.engine
	r.engineMu.Unlock()

	switch {
	// === states that do not require further processing ===
	case condition == nil:
		// The extension does not require extraction yet.
		return nil
	case condition.Reason == v1alpha1.ExtensionExtractedReasonCompleted:
		if condition.Status == metav1.ConditionTrue {
			return nil
		}
		return r.setExtractedCondition(ctx, ext, metav1.ConditionTrue,
			v1alpha1.ExtensionExtractedReasonCompleted, "Extraction completed successfully")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonFailed:
		// Do nothing; the message has already been set.
		return nil
	// === states that require further processing ===
	case engine == nil:
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonEngineNotReady, "The container engine is not ready")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonEngineNotReady:
		// Given the previous branch, the engine is ready.
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, "The container engine became ready")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonPreparing:
		return r.extractor.prepare(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonMetadata:
		return r.extractor.extractMetadata(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonIcon:
		return r.extractor.extractIcon(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonUI:
		return r.extractor.extractUI(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonExecutable:
		return r.extractor.extractExecutable(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonContainer:
		return r.extractor.extractContainer(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonFinishing:
		if state, ok := r.extractor.state.LoadAndDelete(ext.GetUID()); ok {
			state.destroy()
		}
		return r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonCompleted, "Extraction completed successfully")
	default:
		// Should not be reachable.
		log.Error(errors.New("unexpected condition"),
			"unexpected extraction condition reason",
			"reason", condition.Reason)
		return fmt.Errorf("unexpected extraction condition reason: %s", condition.Reason)
	}
}

func (r *ExtensionReconciler) setExtractedCondition(ctx context.Context, ext *v1alpha1.Extension, status metav1.ConditionStatus, reason, message string) error {
	return r.setCondition(ctx, ext, v1alpha1.ExtensionConditionExtracted, status, reason, message)
}
