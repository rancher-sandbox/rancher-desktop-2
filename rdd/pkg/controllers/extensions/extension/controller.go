// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package extension registers the Rancher Desktop Extension controller.
package extension

import (
	"context"
	_ "embed"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/extensions/extension/controllers"
)

func init() {
	base.RegisterController(newController())
}

// ControllerName is the name of this controller.
const ControllerName = "extension"

// APIGroup is the API group this controller belongs to.
const APIGroup = "extensions"

//go:embed crd.yaml
var extensionCRD string

const (
	// extensionValidatorWebhookName is the name used for the Extension validating webhook.
	extensionValidatorWebhookName = "extension-validator.extensions.rancherdesktop.io"
	// extensionValidatorConfigName is the name of the Extension ValidatingWebhookConfiguration.
	extensionValidatorConfigName = "extension-validator"
)

// controller implements the base.Controller interface for extension.
type controller struct {
	webhookPort     int
	webhookManagers []base.WebhookManager
}

// Verify that controller implements base.Controller and base.WebhookController interfaces.
var (
	_ base.Controller        = &controller{}
	_ base.WebhookController = &controller{}
)

func newController() base.Controller {
	return &controller{}
}

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
	return extensionCRD
}

// SetWebhookPort implements [base.WebhookController].
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

// setupWebhook sets up the Extension validating webhook.
func (c *controller) setupWebhook(mgr ctrl.Manager) error {
	validatingConfig := base.WebhookConfig[*v1alpha1.Extension]{
		Name:        extensionValidatorConfigName,
		WebhookName: extensionValidatorWebhookName,
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
		},
		Validator: &controllers.ExtensionValidator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.Extension{}, validatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)
	return nil
}

// RegisterWithManager implements the complete controller registration for both
// embedded and external modes.  It registers the CRD types with the scheme,
// sets up the reconciler and the validating webhook.
func (c *controller) RegisterWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := containersv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	if err := (&controllers.ExtensionInstalledReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&controllers.ExtensionStartedReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := controllers.NewExtensionExtractedReconciler(ctx, mgr).SetupWithManager(mgr); err != nil {
		return err
	}

	if err := (&controllers.ExtensionReadyReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	return c.setupWebhook(mgr)
}
