// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// reconcileApp reacts to changes to the app, maintaining the engine state.
func (r *ExtensionReconciler) reconcileApp(ctx context.Context, app *appv1alpha1.App) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("not implemented")
}
