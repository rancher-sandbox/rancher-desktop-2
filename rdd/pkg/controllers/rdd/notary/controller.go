// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package notary

import (
	"context"
	_ "embed"

	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/notary/controllers"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "notary"

// APIGroup is the API group this controller belongs to.
const APIGroup = "rdd"

// Webhook configuration constants.
const (
	// webhookName is the name used for the webhook configuration.
	webhookName = "notary.rdd.rancherdesktop.io"
	// validatorConfigName is the name of the ValidatingWebhookConfiguration.
	validatorConfigName = "notary-validator"
)

//go:embed crd.yaml
var notaryCRD string

// controller implements the base.Controller interface for notary.
type controller struct {
	webhookPort     int                   // The actual webhook port allocated by SharedControllerManager
	webhookManagers []base.WebhookManager // WebhookManagers for parallel setup
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

// GetCRDData returns the embedded CRD YAML data.
func (c *controller) GetCRDData() string {
	return notaryCRD
}

// setupReconciler sets up the NotaryReconciler with the manager.
func (c *controller) setupReconciler(mgr ctrl.Manager) error {
	return (&controllers.NotaryReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder(ControllerName + "-controller"),
		Manager:  mgr,
	}).SetupWithManager(mgr)
}

// setupWebhookWithRuntimeConfig sets up webhook with shared certificate configuration.
func (c *controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	webhookConfig := base.WebhookConfig[*v1alpha1.Notary]{
		Name:        validatorConfigName,
		WebhookName: webhookName,
		WebhookPort: c.webhookPort,
		Validator:   &validator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.Notary{}, webhookConfig)
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
	if err := c.setupReconciler(mgr); err != nil {
		return err
	}
	return c.setupWebhookWithRuntimeConfig(mgr)
}

// validator validates Notary resources via webhook (for external controllers).
type validator struct{}

var _ ctrlwebhookadmission.Validator[*v1alpha1.Notary] = &validator{}

func (v *validator) ValidateCreate(ctx context.Context, notary *v1alpha1.Notary) (ctrlwebhookadmission.Warnings, error) {
	return v.validateNotary(ctx, notary)
}

func (v *validator) ValidateUpdate(ctx context.Context, _, newNotary *v1alpha1.Notary) (ctrlwebhookadmission.Warnings, error) {
	return v.validateNotary(ctx, newNotary)
}

func (v *validator) ValidateDelete(context.Context, *v1alpha1.Notary) (ctrlwebhookadmission.Warnings, error) {
	// Allow all deletions
	return nil, nil
}

// validateNotary performs the actual validation logic.
func (v *validator) validateNotary(ctx context.Context, notary *v1alpha1.Notary) (ctrlwebhookadmission.Warnings, error) {
	// Check if this is a dry run request
	if base.IsDryRun(ctx) {
		klog.V(1).Infof("[DryRun] Webhook validating Notary %s/%s\n", notary.Namespace, notary.Name)
	}
	return ValidateNotary(notary)
}
