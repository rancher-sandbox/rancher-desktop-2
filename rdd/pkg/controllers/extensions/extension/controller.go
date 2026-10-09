// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package extension registers the Rancher Desktop Extension controller.
package extension

import (
	"context"
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
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

	return nil
}
