// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "compose"

// APIGroup is the API group this controller belongs to.
const APIGroup = "containers"

//go:embed crd.yaml
var controllerCRD string

// controller implements [base.Controller] for compose.
// It only registers a validating webhook; there is no reconciler yet.
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

// setupWebhookWithRuntimeConfig registers a validating webhook that enforces
// spec.namespace/spec.name-derived naming on ComposeUpRequest create.
func (c *controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up compose project webhook")
	validatingConfig := base.WebhookConfig[*v1alpha1.ComposeUpRequest]{
		Name:        "compose-up-request-validating",
		WebhookName: "compose-up-request-validating.containers.rancherdesktop.io",
		WebhookPort: c.webhookPort,
		Validator: &composeUpRequestValidator{
			Client: mgr.GetClient(),
			Reader: mgr.GetAPIReader(),
		},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.ComposeUpRequest{}, validatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	return nil
}

// RegisterWithManager implements [base.Controller].
func (c *controller) RegisterWithManager(_ context.Context, mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	if err := c.setupWebhookWithRuntimeConfig(mgr); err != nil {
		mgr.GetLogger().Error(err, "Failed to set up compose project webhook")
		return err
	}

	return nil
}
