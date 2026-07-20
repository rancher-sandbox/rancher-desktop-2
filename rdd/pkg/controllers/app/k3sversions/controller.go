// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package k3sversions manages the k3s-versions config map, providing
// information about supported Kubernetes versions.
package k3sversions

import (
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	v1 "k8s.io/api/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/k3sversions/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(newController())
}

// ControllerName is the name of this controller.
const ControllerName = "k3s-versions"

// APIGroup is the API group this controller belongs to.
const APIGroup = "app"

const (
	// k3sVersionsValidatorWebhookName is the name used for the k3s-versions validating webhook.
	k3sVersionsValidatorWebhookName = "k3s-versions-validator.app.rancherdesktop.io"
	// k3sVersionsValidatorConfigName is the name of the k3s-versions ValidatingWebhookConfiguration.
	k3sVersionsValidatorConfigName = "k3s-versions-validator"
)

// controller implements the base.Controller interface for app.
type controller struct {
	webhookPort     int
	webhookManagers []base.WebhookManager
}

// Verify that controller implements desired interfaces.
var (
	_ base.WebhookController = &controller{}
	_ base.Controller        = &controller{}
)

func newController() base.Controller {
	return &controller{}
}

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
	return ""
}

// SetWebhookPort sets the webhook port allocated by SharedControllerManager.
func (c *controller) SetWebhookPort(port int) {
	c.webhookPort = port
}

// GetWebhookServiceName returns the DNS service name for webhook certificates.
func (c *controller) GetWebhookServiceName() string {
	return ControllerName + "-webhook"
}

// GetWebhookManagers returns all WebhookManagers for parallel setup.
func (c *controller) GetWebhookManagers() []base.WebhookManager {
	return c.webhookManagers
}

// setupWebhook sets up the k3s-versions validating webhook.
func (c *controller) setupWebhook(mgr ctrl.Manager) error {
	validatingConfig := base.WebhookConfig[*v1.ConfigMap]{
		Name:        k3sVersionsValidatorConfigName,
		WebhookName: k3sVersionsValidatorWebhookName,
		WebhookPort: c.webhookPort,
		ObjectSelector: metav1apply.LabelSelector().
			WithMatchLabels(controllers.DesiredLabels()),
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Delete,
		},
		Validator: &controllers.K3sVersionsValidator{Client: mgr.GetClient()},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1.ConfigMap{}, validatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)
	return nil
}

// RegisterWithManager implements the complete controller registration for both
// embedded and external modes.  It sets up the controller with the manager.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	reconciler := controllers.K3sVersionsReconciler{Client: mgr.GetClient()}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		return err
	}

	return c.setupWebhook(mgr)
}
