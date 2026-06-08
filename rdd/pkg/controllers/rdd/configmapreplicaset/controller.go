// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package configmapreplicaset registers the ConfigMapReplicaSet controller. A
// ConfigMapReplicaSet declaratively maintains a set of ConfigMaps with identical
// data, operating at the API level without any pod dependencies.
package configmapreplicaset

import (
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/configmapreplicaset/controllers"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "configmapreplicaset"

// APIGroup is the API group this controller belongs to.
const APIGroup = "rdd"

//go:embed crd.yaml
var configMapReplicaSetCRD string

// controller implements the base.Controller interface for configmapreplicaset.
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
	return configMapReplicaSetCRD
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	// Create and set up the controller with the manager
	return (&controllers.ConfigMapReplicaSetReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Manager: mgr,
	}).SetupWithManager(mgr)
}
