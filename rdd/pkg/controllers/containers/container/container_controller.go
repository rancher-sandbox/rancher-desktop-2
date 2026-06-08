// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package container

import (
	_ "embed"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "container"

// APIGroup is the API group this controller belongs to.
const APIGroup = "containers"

//go:embed crd.yaml
var controllerCRD string

// controller implements the base.Controller interface for container.
type controller struct {
	webhookPort     int
	webhookManagers []base.WebhookManager
}

// Verify that controller implements base.Controller and base.WebhookController interfaces.
var (
	_ base.Controller        = &controller{}
	_ base.WebhookController = &controller{}
)

// GetName returns the controller name.
func (c *controller) GetName() string {
	return ControllerName
}

// GetAPIGroup returns the API group this controller belongs to.
func (c *controller) GetAPIGroup() string {
	return APIGroup
}

func (c *controller) SetWebhookPort(port int) {
	c.webhookPort = port
}

// GetWebhookServiceName implements base.WebhookController.
func (c *controller) GetWebhookServiceName() string {
	return ControllerName + "-webhook"
}

// GetWebhookManagers implements base.WebhookController.
func (c *controller) GetWebhookManagers() []base.WebhookManager {
	return c.webhookManagers
}

// GetCRDData returns the embedded CRD YAML data.
func (c *controller) GetCRDData() string {
	return controllerCRD
}

// setupReconciler sets up the reconciler with the manager.
func (c *controller) setupReconciler(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up reconciler")
	return (&reconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder(ControllerName + "-controller"),
	}).SetupWithManager(mgr)
}

// setupWebhookWithRuntimeConfig registers a webhook that enforces an immutable
// spec, rejects action annotations on create, and validates action annotations
// on update.
func (c *controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up container webhook")
	mutatingConfig := base.WebhookConfig[*v1alpha1.Container]{
		Name:        "container-mutating",
		WebhookName: "container-mutating.containers.rancherdesktop.io",
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
		},
		Validator: &immutableValidator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.Container{}, mutatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	return nil
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	// Create and set up the controller with the manager
	if err := c.setupReconciler(mgr); err != nil {
		mgr.GetLogger().Error(err, "Failed to set up containers reconcilers")
		return err
	}
	return c.setupWebhookWithRuntimeConfig(mgr)
}
