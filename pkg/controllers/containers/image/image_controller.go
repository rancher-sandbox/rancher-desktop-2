// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package image registers the Image controller. Images reflect container engine
// images as Kubernetes resources, with each tag represented as a separate Image
// object.
package image

import (
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "image"

// APIGroup is the API group this controller belongs to.
const APIGroup = "containers"

//go:embed crd.yaml
var controllerCRD string

// controller implements the base.Controller interface for image.
// This effectively only exists to store the CRD data.
type controller struct{}

// Verify that controller implements base.Controller and base.WebhookController interfaces.
var (
	_ base.Controller = &controller{}
)

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
	return controllerCRD
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	return v1alpha1.AddToScheme(mgr.GetScheme())
}
