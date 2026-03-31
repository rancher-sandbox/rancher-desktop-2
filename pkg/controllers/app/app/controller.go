// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package app registers the App controller. The App controller manages the
// cluster-scoped App singleton that represents the Rancher Desktop application;
// it creates and owns a LimaVM and mirrors its conditions back to App status.
package app

import (
	_ "embed"
	"runtime"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/app/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// Embedded Lima template split into platform-specific images and shared
// configuration. The two parts are concatenated at runtime so the VM gets
// an image type compatible with the host (qcow2 on Unix, tarball on WSL2).
//
//go:embed lima-images-unix.yaml
var limaImagesUnix string

//go:embed lima-images-wsl2.yaml
var limaImagesWSL2 string

//go:embed lima-template.yaml
var limaTemplate string

func limaTemplateData() string {
	images := limaImagesUnix
	if runtime.GOOS == "windows" {
		images = limaImagesWSL2
	}
	return images + limaTemplate
}

func init() {
	base.RegisterController(newController())
}

// ControllerName is the name of this controller.
const ControllerName = "app"

// APIGroup is the API group this controller belongs to.
const APIGroup = "app"

//go:embed crd.yaml
var appCRD string

// controller implements the base.Controller interface for app.
type controller struct{}

func newController() base.Controller {
	return &controller{}
}

// Verify that controller implements base.Controller interface.
var _ base.Controller = &controller{}

// GetName returns the controller name.
func (c *controller) GetName() string {
	return ControllerName
}

// GetAPIGroup returns the API group this controller belongs to.
func (c *controller) GetAPIGroup() string {
	return APIGroup
}

// GetCRDData returns the embedded CRD YAML data.
func (c *controller) GetCRDData() string {
	return appCRD
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
// It registers the CRD types with the scheme and sets up the controller with the manager.
// It also registers Lima types, which allows App controller to create and watch LimaVM resources.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := limav1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	return (&controllers.AppReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		LimaTemplateData: limaTemplateData(),
	}).SetupWithManager(mgr)
}
