// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package pathmanagement registers the PATH management controller. The
// controller adds or removes the Rancher Desktop bin directory
// (~/.rd<suffix>/bin) from the user's PATH according to spec.application.addPath,
// by editing shell startup files on Unix and the user Environment on Windows.
package pathmanagement

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/pathmanagement/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func init() {
	base.RegisterController(&controller{})
}

// Unwind removes this instance's PATH edits (shell startup files on Unix, the
// user Environment on Windows). Service deletion calls it before removing the bin
// directory, so a stale block isn't left behind pointing at a directory that no
// longer exists. It runs host-side and needs no API client.
func Unwind(ctx context.Context) error {
	r := &controllers.PathManagementReconciler{
		BinDir: instance.BinDir(),
		Suffix: instance.Suffix(),
	}
	return r.Unwind(ctx)
}

type controller struct{}

var _ base.Controller = &controller{}

func (c *controller) GetName() string {
	return appv1alpha1.PathManagementControllerName
}

func (c *controller) GetAPIGroup() string {
	// We only use the app CRD, so there is no extra group to register.
	return ""
}

func (c *controller) GetCRDData() string {
	return ""
}

func (c *controller) RegisterWithManager(_ context.Context, mgr ctrl.Manager) error {
	if err := appv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	r := &controllers.PathManagementReconciler{
		Client: mgr.GetClient(),
		BinDir: instance.BinDir(),
		Suffix: instance.Suffix(),
	}
	return r.SetupWithManager(mgr)
}
