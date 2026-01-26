// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package demo

import (
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/demo/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "demo"

// APIGroup is the API group this controller belongs to.
const APIGroup = "app"

//go:embed crd.yaml
var demoCRD string

// controller implements the base.Controller interface for demo.
type controller struct{}

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
	return demoCRD
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	// Create and set up the controller with the manager
	return (&controllers.DemoReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(ControllerName + "-controller"), //nolint:staticcheck // new API requires events.k8s.io
	}).SetupWithManager(mgr)
}
