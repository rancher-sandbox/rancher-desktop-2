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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// reconcileApp reacts to changes to the app, maintaining the engine state.
func (r *ExtensionReconciler) reconcileApp(ctx context.Context, app *appv1alpha1.App) (ctrl.Result, error) {
	var engine engine
	var err error
	switch {
	case app == nil,
		!apimeta.IsStatusConditionTrue(app.Status.Conditions, appv1alpha1.AppConditionContainerEngineReady):
		r.engineMu.Lock()
		oldEngine := r.engine
		r.engine = nil
		r.engineMu.Unlock()
		if oldEngine != nil {
			_ = oldEngine.disconnect(ctx)
		}
	case app.Spec.ContainerEngine.Name == "moby":
		engine, err = r.setEngine(ctx, &dockerEngine{})
	case app.Spec.ContainerEngine.Name == "containerd":
		return ctrl.Result{}, errors.New("not implemented: containerd engine")
	}

	if err != nil {
		return ctrl.Result{}, err
	}

	// Update extensions to note the container engines are ready.
	var extensions v1alpha1.ExtensionList
	if err := r.List(ctx, &extensions); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var errs []error
	for _, ext := range extensions.Items {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest v1alpha1.Extension
			if err := r.Get(ctx, client.ObjectKeyFromObject(&ext), &latest); err != nil {
				return client.IgnoreNotFound(err)
			}

			condition := metav1.Condition{
				Type:    v1alpha1.ExtensionConditionContainerEngineReady,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ExtensionContainerEngineReadyReasonNotReady,
				Message: "Container engine not ready",
				// Omit ObservedGeneration, because we never checked the extension.
			}
			if engine != nil {
				condition.Status = metav1.ConditionTrue
				condition.Reason = v1alpha1.ExtensionContainerEngineReadyReasonReady
				condition.Message = fmt.Sprintf("The %s engine is ready", app.Spec.ContainerEngine.Name)
			}
			if apimeta.SetStatusCondition(&latest.Status.Conditions, condition) {
				return r.Status().Update(ctx, &latest)
			}
			return nil
		})
		errs = append(errs, err)
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// Create a new engine of type T and set it; if the engine is already the same
// type, then this is a no-op.  Returns the active engine.
func (r *ExtensionReconciler) setEngine[T engine](ctx context.Context, newEngine T) (engine, error) {
	r.engineMu.Lock()
	oldEngine := r.engine
	if _, ok := oldEngine.(T); ok {
		r.engineMu.Unlock()
		return oldEngine, nil
	}
	r.engine = nil
	r.engineMu.Unlock()

	if oldEngine != nil {
		if err := oldEngine.disconnect(ctx); err != nil {
			return nil, err
		}
	}
	if err := newEngine.connect(ctx); err != nil {
		return nil, err
	}
	r.engineMu.Lock()
	if r.engine != nil {
		r.engineMu.Unlock()
		err := newEngine.disconnect(ctx)
		return nil, errors.Join(errors.New("engine was modified concurrently"), err)
	}
	r.engine = newEngine
	r.engineMu.Unlock()
	return newEngine, nil
}
