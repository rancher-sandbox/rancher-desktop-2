// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package limavm

import (
	"context"
	_ "embed"
	"fmt"
	"os"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/lima/limavm/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "limavm"

// APIGroup is the API group this controller belongs to.
const APIGroup = "lima"

// Webhook configuration constants.
const (
	// limaVMDefaulterWebhookName is the name used for the LimaVM defaulting webhook.
	limaVMDefaulterWebhookName = "limavm-defaulter.lima.rancherdesktop.io"
	// limaVMDefaulterConfigName is the name of the LimaVM MutatingWebhookConfiguration.
	limaVMDefaulterConfigName = "limavm-defaulter"

	// configMapValidatorWebhookName is the name used for the ConfigMap validation webhook.
	configMapValidatorWebhookName = "limavm-configmap-validator.lima.rancherdesktop.io"
	// configMapValidatorConfigName is the name of the ConfigMap ValidatingWebhookConfiguration.
	configMapValidatorConfigName = "limavm-configmap-validator"
)

//go:embed crd.yaml
var limaCRD string

// controller implements the base.Controller interface for limavm.
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
	return limaCRD
}

// setupReconciler sets up the LimaVMReconciler with the manager.
func (c *controller) setupReconciler(mgr ctrl.Manager) error {
	return (&controllers.LimaVMReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Manager:  mgr,
		Recorder: mgr.GetEventRecorder(ControllerName + "-controller"),
	}).SetupWithManager(mgr)
}

// setupWebhookWithRuntimeConfig sets up webhooks with shared certificate configuration.
func (c *controller) setupWebhookWithRuntimeConfig(mgr ctrl.Manager) error {
	// Set up LimaVM mutating webhook (validates uniqueness and creates template ConfigMap during admission)
	sideEffectsNoneOnDryRun := admissionregistrationv1.SideEffectClassNoneOnDryRun
	mutatingConfig := base.WebhookConfig[*v1alpha1.LimaVM]{
		Name:        limaVMDefaulterConfigName,
		WebhookName: limaVMDefaulterWebhookName,
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create, // Only on CREATE - UPDATE doesn't need to create ConfigMap again
		},
		SideEffects: &sideEffectsNoneOnDryRun, // Creates ConfigMap normally, but not during dry-run
		Defaulter:   &defaulter{Client: mgr.GetClient(), Reader: mgr.GetAPIReader()},
	}

	managers, err := base.SetupWebhookForResource(mgr, &v1alpha1.LimaVM{}, mutatingConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	// Set up ConfigMap webhook for template ConfigMaps
	configMapWebhookConfig := base.WebhookConfig[*corev1.ConfigMap]{
		Name:        configMapValidatorConfigName,
		WebhookName: configMapValidatorWebhookName,
		WebhookPort: c.webhookPort,
		ObjectSelector: metav1apply.LabelSelector().
			WithMatchLabels(map[string]string{
				controllers.TemplateConfigMapLabel: "true",
			}),
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
			admissionregistrationv1.Delete,
		},
		Validator: &ConfigMapValidator{Client: mgr.GetClient()},
	}

	managers, err = base.SetupWebhookForResource(mgr, &corev1.ConfigMap{}, configMapWebhookConfig)
	if err != nil {
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	return nil
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Set LIMA_HOME for the Lima library to use the correct instance directory.
	// This must be set before any Lima operations are performed.
	if err := os.Setenv("LIMA_HOME", instance.LimaHome()); err != nil {
		return fmt.Errorf("failed to set LIMA_HOME: %w", err)
	}
	klog.Infof("Set LIMA_HOME to %s", instance.LimaHome())

	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := c.setupReconciler(mgr); err != nil {
		return err
	}
	return c.setupWebhookWithRuntimeConfig(mgr)
}

// defaulter handles LimaVM mutation via webhook.
// It validates cross-namespace name uniqueness, then creates the template ConfigMap synchronously during admission.
type defaulter struct {
	Client client.Client
	// Reader bypasses the informer cache to read directly from the API server.
	// The templateRef ConfigMap may have been created moments before the LimaVM,
	// so the informer cache may not have synced it yet.
	Reader client.Reader
}

var _ ctrlwebhookadmission.Defaulter[*v1alpha1.LimaVM] = &defaulter{}

// Default is called during CREATE operations to add finalizer and create the template ConfigMap.
func (d *defaulter) Default(ctx context.Context, limavm *v1alpha1.LimaVM) error {
	controllerutil.AddFinalizer(limavm, base.CleanupFinalizerName)

	// Validate name uniqueness BEFORE creating ConfigMap
	// This prevents orphaned ConfigMaps when validation fails
	if _, err := validateLimaVM(ctx, d.Client, limavm); err != nil {
		return err
	}

	// Fetch template data from templateRef
	templateData, err := d.fetchTemplateRefData(ctx, limavm)
	if err != nil {
		return fmt.Errorf("failed to fetch template data: %w", err)
	}

	// Create template ConfigMap with label
	templateConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      limavm.GetTemplateConfigMapName(),
			Namespace: limavm.Namespace,
			Labels: map[string]string{
				controllers.TemplateConfigMapLabel: "true",
			},
			Finalizers: []string{
				base.OwnedFinalizerName,
			},
		},
		Data: map[string]string{
			v1alpha1.TemplateConfigMapKey: templateData,
		},
	}

	var options []client.CreateOption
	var dryRunText string
	if base.IsDryRun(ctx) {
		options = append(options, client.DryRunAll)
		dryRunText = "[DryRun] "
	}

	// Create the ConfigMap (this triggers ConfigMap admission webhook for validation)
	if err := d.Client.Create(ctx, templateConfigMap, options...); err != nil {
		return fmt.Errorf("failed to create template ConfigMap: %w", err)
	}

	klog.Infof("%sCreated template ConfigMap %s/%s for LimaVM %s/%s",
		dryRunText, limavm.Namespace, limavm.GetTemplateConfigMapName(), limavm.Namespace, limavm.Name)

	// Note: The reconciler will set owner reference and status after LimaVM creation:
	// - Owner reference requires LimaVM UID (not available during admission)
	// - Status subresource cannot be modified by mutating webhooks
	return nil
}

// fetchTemplateRefData fetches the template data from the templateRef ConfigMap.
func (d *defaulter) fetchTemplateRefData(ctx context.Context, limavm *v1alpha1.LimaVM) (string, error) {
	configMapKey := types.NamespacedName{
		Name:      limavm.Spec.TemplateRef.Name,
		Namespace: limavm.Spec.TemplateRef.Namespace,
	}
	if configMapKey.Namespace == "" {
		configMapKey.Namespace = limavm.Namespace
	}
	configMap := &corev1.ConfigMap{}
	if err := d.Reader.Get(ctx, configMapKey, configMap); err != nil {
		return "", fmt.Errorf("failed to get templateRef ConfigMap %q in namespace %q: %w", configMapKey.Name, configMapKey.Namespace, err)
	}
	return configMap.Data[v1alpha1.TemplateConfigMapKey], nil
}

// ConfigMapValidator validates ConfigMap resources that are template ConfigMaps for LimaVM resources.
// It is only invoked for ConfigMaps that have the TemplateConfigMapLabel set to "true".
type ConfigMapValidator struct {
	Client client.Client
}

var _ ctrlwebhookadmission.Validator[*corev1.ConfigMap] = &ConfigMapValidator{}

func (v *ConfigMapValidator) ValidateCreate(ctx context.Context, configMap *corev1.ConfigMap) (ctrlwebhookadmission.Warnings, error) {
	return v.validateTemplateConfigMap(ctx, configMap)
}

func (v *ConfigMapValidator) ValidateUpdate(ctx context.Context, _, newConfigMap *corev1.ConfigMap) (ctrlwebhookadmission.Warnings, error) {
	return v.validateTemplateConfigMap(ctx, newConfigMap)
}

func (v *ConfigMapValidator) ValidateDelete(ctx context.Context, configMap *corev1.ConfigMap) (ctrlwebhookadmission.Warnings, error) {
	if base.IsDryRun(ctx) {
		klog.V(1).Infof("[DryRun] Webhook validating ConfigMap deletion %s/%s\n", configMap.Namespace, configMap.Name)
	}
	if base.HasOwnedFinalizer(configMap) {
		return nil, fmt.Errorf("cannot delete template ConfigMap %q: it is protected by the LimaVM controller; delete the owning LimaVM resource instead", configMap.Name)
	}
	return nil, nil
}

// validateTemplateConfigMap validates the template data in a ConfigMap.
func (v *ConfigMapValidator) validateTemplateConfigMap(ctx context.Context, configMap *corev1.ConfigMap) (ctrlwebhookadmission.Warnings, error) {
	if base.IsDryRun(ctx) {
		klog.V(1).Infof("[DryRun] Webhook validating template ConfigMap %s/%s\n", configMap.Namespace, configMap.Name)
	}
	return validateTemplateData(ctx, configMap.Data)
}
