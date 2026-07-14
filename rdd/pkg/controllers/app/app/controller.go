// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package app registers the App controller. The App controller manages the
// cluster-scoped App singleton that represents the Rancher Desktop application;
// it creates and owns a LimaVM and mirrors its conditions back to App status.
package app

import (
	_ "embed"
	"fmt"
	"runtime"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/app/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostinfo"
	servicecontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// Embedded Lima template split into platform-specific images and shared
// configuration. The two parts are concatenated at runtime so the VM gets
// an image type compatible with the host (qcow2 on Unix, tarball on WSL2).
//
//go:embed lima-images-unix.yaml
var limaImagesUnix string

//go:embed lima-images-wsl2.yaml
var limaImagesWSL2 string

//go:embed lima-template.yaml
var limaTemplate string

//go:embed k3s-versions.json
var k3sVersionsData string

func limaTemplateData() string {
	images := limaImagesUnix
	if runtime.GOOS == "windows" {
		images = limaImagesWSL2
	}
	return images + limaTemplate
}

func init() {
	base.RegisterController(newController())
}

// ControllerName is the name of this controller.
const ControllerName = "app"

// APIGroup is the API group this controller belongs to.
const APIGroup = "app"

//go:embed crd.yaml
var appCRD string

const (
	// appValidatorWebhookName is the name used for the App validating webhook.
	appValidatorWebhookName = "app-validator.app.rancherdesktop.io"
	// appValidatorConfigName is the name of the App ValidatingWebhookConfiguration.
	appValidatorConfigName = "app-validator"
	// appDefaulterWebhookName is the name used for the App mutating webhook.
	appDefaulterWebhookName = "app-defaulter.app.rancherdesktop.io"
	// appDefaulterConfigName is the name of the App MutatingWebhookConfiguration.
	appDefaulterConfigName = "app-defaulter"
)

// controller implements the base.Controller interface for app.
type controller struct {
	webhookPort     int
	webhookManagers []base.WebhookManager
}

// Verify that controller implements base.WebhookController interface.
var _ base.WebhookController = &controller{}

func newController() base.Controller {
	return &controller{}
}

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
	return appCRD
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

// setupWebhook sets up the App mutating and validating webhooks. The mutating
// webhook resolves Kubernetes version channels (e.g. "stable") to concrete
// versions before the validating webhook checks them.
func (c *controller) setupWebhook(mgr ctrl.Manager) error {
	hostInfo := hostinfo.Detect()
	defaulter, err := controllers.NewAppDefaulter(k3sVersionsData, hostInfo)
	if err != nil {
		return err
	}
	mutatingConfig := base.WebhookConfig[*v1alpha1.App]{
		Name:        appDefaulterConfigName,
		WebhookName: appDefaulterWebhookName,
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
		},
		Defaulter: defaulter,
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.App{}, mutatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	validator, err := controllers.NewAppValidator(k3sVersionsData, hostInfo)
	if err != nil {
		return err
	}
	validatingConfig := base.WebhookConfig[*v1alpha1.App]{
		Name:        appValidatorConfigName,
		WebhookName: appValidatorWebhookName,
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
		},
		Validator: validator,
	}

	managers, err = base.SetupWebhookForResource(mgr, &v1alpha1.App{}, validatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)
	return nil
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
// It registers the CRD types with the scheme and sets up the controller with the manager.
// It also registers Lima types, which allows App controller to create and watch LimaVM resources.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := limav1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	discovery, err := servicecontrollers.NewControllerManagerDiscovery(mgr.GetConfig())
	if err != nil {
		return fmt.Errorf("failed to create controller-manager discovery: %w", err)
	}

	if err := (&controllers.AppReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		LimaTemplateData: limaTemplateData(),
		Discovery:        discovery,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	return c.setupWebhook(mgr)
}
