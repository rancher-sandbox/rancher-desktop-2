// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package containernamespace

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
const ControllerName = "containernamespace"

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

// GetName implements [base.Controller].
func (c *controller) GetName() string {
	return ControllerName
}

// GetAPIGroup implements [base.Controller].
func (c *controller) GetAPIGroup() string {
	return APIGroup
}

// GetCRDData returns the embedded CRD YAML data, implementing [base.Controller].
func (c *controller) GetCRDData() string {
	return controllerCRD
}

// SetWebhookPort provides the actual webhook port to the controller, implementing [base.WebhookController].
func (c *controller) SetWebhookPort(port int) {
	c.webhookPort = port
}

// GetWebhookServiceName implements [base.WebhookController].
func (c *controller) GetWebhookServiceName() string {
	return ControllerName + "-webhook"
}

// GetWebhookManagers implements [base.WebhookController].
func (c *controller) GetWebhookManagers() []base.WebhookManager {
	return c.webhookManagers
}

// set up the container controller with a webhook which prevents all modification.
func (c *controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up container namespace webhook")
	mutatingConfig := base.WebhookConfig[*v1alpha1.ContainerNamespace]{
		Name:        "container-namespace-deleting",
		WebhookName: "container-namespace-deleting.containers.rancherdesktop.io",
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Delete,
		},
		Validator: &deleteValidator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.ContainerNamespace{}, mutatingConfig)
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

	return c.setupWebhookWithRuntimeConfig(mgr)
}
