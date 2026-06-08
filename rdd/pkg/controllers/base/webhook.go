// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	admissionregistrationv1apply "k8s.io/client-go/applyconfigurations/admissionregistration/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// IsDryRun checks if the current admission request is a dry-run.
// It returns true if the request context contains a dry-run admission request.
func IsDryRun(ctx context.Context) bool {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return false
	}
	return req.DryRun != nil && *req.DryRun
}

// OwnedDeletionGuard is a generic validating webhook that rejects DELETE requests
// on resources with an owned finalizer. It implements admission.Validator[T] so it
// can be used directly as the Validator in a WebhookConfig, or embedded in a
// validator that needs additional create/update validation.
type OwnedDeletionGuard[T client.Object] struct{}

// ValidateCreate allows all create requests.
func (g *OwnedDeletionGuard[T]) ValidateCreate(_ context.Context, _ T) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate allows all update requests.
func (g *OwnedDeletionGuard[T]) ValidateUpdate(_ context.Context, _, _ T) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete rejects deletion of resources that still have an owned finalizer.
func (g *OwnedDeletionGuard[T]) ValidateDelete(ctx context.Context, obj T) (admission.Warnings, error) {
	if IsDryRun(ctx) {
		klog.V(1).Infof("[DryRun] Webhook validating delete of %s/%s", obj.GetNamespace(), obj.GetName())
	}
	if owner := OwnedFinalizerOwner(obj); owner != "" {
		return nil, fmt.Errorf("cannot delete %q: owned by %s; delete the %s resource instead", obj.GetName(), owner, owner)
	}
	return nil, nil
}

// GenerateWebhookPath generates the webhook path that controller-runtime uses
// when registering a webhook for a given GroupVersionKind and webhook type.
// This follows controller-runtime's conventions:
// - Validating: /validate-{group}-{version}-{kind}
// - Mutating: /mutate-{group}-{version}-{kind}
// Dots in the API group are replaced with dashes, and Kind is lowercased.
//
// Example: GenerateWebhookPath(schema.GroupVersionKind{Group: "lima.rancherdesktop.io", Version: "v1alpha1", Kind: "LimaVM"}, MutatingWebhook)
// Returns: "/mutate-lima-rancherdesktop-io-v1alpha1-limavm".
func GenerateWebhookPath(gvk schema.GroupVersionKind, webhookType WebhookType) string {
	group := strings.ReplaceAll(gvk.Group, ".", "-")
	kind := strings.ToLower(gvk.Kind)

	var prefix string
	if webhookType == MutatingWebhook {
		prefix = "mutate"
	} else {
		prefix = "validate"
	}

	return fmt.Sprintf("/%s-%s-%s-%s", prefix, group, gvk.Version, kind)
}

// WebhookType indicates whether this is a validating or mutating webhook.
type WebhookType string

const (
	// ValidatingWebhook indicates this is a validating admission webhook.
	ValidatingWebhook WebhookType = "validating"
	// MutatingWebhook indicates this is a mutating admission webhook.
	MutatingWebhook WebhookType = "mutating"
)

// WebhookConfig contains configuration for creating a webhook configuration.
type WebhookConfig[T runtime.Object] struct {
	// Name is the name of the webhook configuration resource
	Name string

	// WebhookName is the name field within the webhook configuration
	WebhookName string

	// WebhookPort is the port the webhook server is listening on
	WebhookPort int

	// FailurePolicy determines how the API server handles webhook failures
	// Default is Fail if not specified
	FailurePolicy *admissionregistrationv1.FailurePolicyType

	// ObjectSelector filters which objects the webhook should intercept based on labels
	// If specified, only objects matching the selector will trigger the webhook
	ObjectSelector *metav1apply.LabelSelectorApplyConfiguration

	// Operations specifies which operations the webhook should intercept
	// Default is Create and Update if not specified
	Operations []admissionregistrationv1.OperationType

	// SideEffects declares whether this webhook has side effects
	// Default is None if not specified
	SideEffects *admissionregistrationv1.SideEffectClass

	// Validator is the custom validator for validating admission webhooks
	Validator admission.Validator[T]

	// Defaulter is the custom defaulter for mutating admission webhooks
	Defaulter admission.Defaulter[T]
}

// WebhookManager handles the creation and management of webhook configurations.
type WebhookManager interface {
	// GetConfigName returns the name of the webhook configuration resource.
	GetConfigName() string

	// GetWebhookType returns the type of webhook (Validating or Mutating).
	GetWebhookType() WebhookType

	// Setup creates the webhook configuration with retry logic.
	// This blocks until the webhook is successfully registered or max attempts are exceeded.
	// This should be called after the webhook server is registered with the manager.
	Setup() error
}

// webhookManagerImpl is the implementation of WebhookManager for a specific resource type.
type webhookManagerImpl[T runtime.Object] struct {
	config      WebhookConfig[T]
	webhookType WebhookType // Validating or Mutating - set by SetupWebhookForResource
	mgr         ctrl.Manager

	// GVK information - used to derive webhook path and API group/version
	gvk schema.GroupVersionKind
	// Plural resource name (e.g., "limavms", "configmaps") - looked up during Setup() via REST mapper
	resources string
}

// GetConfigName returns the name of the webhook configuration resource.
func (wm *webhookManagerImpl[T]) GetConfigName() string {
	return wm.config.Name
}

// GetWebhookType returns the type of webhook (Validating or Mutating).
func (wm *webhookManagerImpl[T]) GetWebhookType() WebhookType {
	return wm.webhookType
}

// SetupWebhookForResource sets up webhook(s) for a resource type.
// It registers the webhook(s) with controller-runtime and creates WebhookManager(s) for later setup.
//
// The config must specify either a validator, a defaulter, or both. The function will create the
// appropriate webhook manager(s) based on which are provided.
func SetupWebhookForResource[T runtime.Object](mgr ctrl.Manager, obj T, config WebhookConfig[T]) ([]WebhookManager, error) {
	if config.Validator == nil && config.Defaulter == nil {
		return nil, errors.New("config must specify at least one of either Validator or Defaulter (or both)")
	}

	// Extract GVK from the scheme type registry.
	// Don't use obj.GetObjectKind() because the ObjectKind may not be filled in yet.
	gvks, _, err := mgr.GetScheme().ObjectKinds(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get object kinds: %w", err)
	}
	if len(gvks) == 0 {
		return nil, fmt.Errorf("no GVK found for object type %T", obj)
	}
	gvk := gvks[0]

	// Build webhook registration with controller-runtime
	builder := ctrl.NewWebhookManagedBy(mgr, obj).WithValidator(config.Validator).WithDefaulter(config.Defaulter)
	if err := builder.Complete(); err != nil {
		return nil, err
	}

	var managers []WebhookManager
	if config.Validator != nil {
		managers = append(managers, &webhookManagerImpl[T]{
			config:      config,
			webhookType: ValidatingWebhook,
			mgr:         mgr,
			gvk:         gvk,
		})
	}
	if config.Defaulter != nil {
		managers = append(managers, &webhookManagerImpl[T]{
			config:      config,
			webhookType: MutatingWebhook,
			mgr:         mgr,
			gvk:         gvk,
		})
	}
	return managers, nil
}

// Setup creates the webhook configuration with retry logic.
// This blocks until the webhook is successfully registered or max attempts are exceeded.
// This should be called after the webhook server is registered with the manager.
func (wm *webhookManagerImpl[T]) Setup() error {
	klog.Info("Starting webhook configuration creation...")

	const maxAttempts = 20
	const retryDelay = 200 * time.Millisecond

	// Resolve the plural resource name from the REST mapper with retry logic
	// This happens here (not during registration) because the API server
	// discovery endpoints may not be ready yet even after manager registration
	if wm.resources == "" {
		klog.Infof("Looking up plural resource name for %s", wm.gvk.String())
		for attempt := 1; ; attempt++ {
			mapping, err := wm.mgr.GetRESTMapper().RESTMapping(wm.gvk.GroupKind(), wm.gvk.Version)
			if err == nil {
				wm.resources = mapping.Resource.Resource
				klog.Infof("Resolved plural resource name: %s (attempt %d)", wm.resources, attempt)
				break
			}
			if attempt >= maxAttempts {
				return fmt.Errorf("failed to get REST mapping for %s after %d attempts: %w", wm.gvk.String(), maxAttempts, err)
			}
			klog.V(2).Infof("REST mapper not ready yet (attempt %d/%d): %v, retrying in %v", attempt, maxAttempts, err, retryDelay)
			time.Sleep(retryDelay)
		}
	}

	// Create the webhook configuration with retry logic for transient API server issues
	for attempt := 1; ; attempt++ {
		klog.Infof("Webhook configuration creation attempt %d/%d", attempt, maxAttempts)
		err := wm.createWebhookConfiguration()
		if err == nil {
			klog.Info("Successfully created webhook configuration")
			return nil
		}
		klog.Errorf("Failed to create webhook configuration (attempt %d/%d): %v", attempt, maxAttempts, err)
		if attempt >= maxAttempts {
			return fmt.Errorf("failed to create webhook configuration after %d attempts", maxAttempts)
		}
		klog.Infof("Waiting %v before retry...", retryDelay)
		time.Sleep(retryDelay)
	}
}

// createWebhookConfiguration creates the webhook configuration (Validating or Mutating).
func (wm *webhookManagerImpl[T]) createWebhookConfiguration() error {
	klog.Infof("Creating %s webhook configuration...", wm.webhookType)
	ctx := context.Background()

	webhookPath := GenerateWebhookPath(wm.gvk, wm.webhookType)
	webhookURL := fmt.Sprintf("https://127.0.0.1:%d%s", wm.config.WebhookPort, webhookPath)
	klog.Infof("Webhook URL: %s", webhookURL)

	// Read CA bundle directly without creating a new certificate manager.
	// The certificates were already created by SharedControllerManager.
	caBundlePath := filepath.Join(instance.TLSDir(), DefaultWebhookCACertFileName)
	klog.Infof("Reading CA bundle from: %s", caBundlePath)
	caBundleBytes, err := os.ReadFile(caBundlePath)
	if err != nil {
		klog.Errorf("Failed to read CA bundle file: %v", err)
		return fmt.Errorf("failed to read CA bundle file %s: %w", caBundlePath, err)
	}

	// Set default failure policy if not specified
	failurePolicy := admissionregistrationv1.Fail
	if wm.config.FailurePolicy != nil {
		failurePolicy = *wm.config.FailurePolicy
	}
	// Set default side effects if not specified
	sideEffects := admissionregistrationv1.SideEffectClassNone
	if wm.config.SideEffects != nil {
		sideEffects = *wm.config.SideEffects
	}
	// Set default operations if not specified
	operations := wm.config.Operations
	if len(operations) == 0 {
		operations = []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create,
			admissionregistrationv1.Update,
		}
	}
	clientConfig := admissionregistrationv1apply.WebhookClientConfig().
		WithURL(webhookURL).
		WithCABundle(caBundleBytes...)
	rules := []*admissionregistrationv1apply.RuleWithOperationsApplyConfiguration{
		admissionregistrationv1apply.RuleWithOperations().
			WithOperations(operations...).
			WithAPIGroups(wm.gvk.Group).
			WithAPIVersions(wm.gvk.Version).
			WithResources(wm.resources),
	}

	// Try to create or update the webhook configuration
	c := wm.mgr.GetClient()
	if c == nil {
		return errors.New("manager client is nil")
	}

	switch wm.webhookType {
	case MutatingWebhook:
		err = wm.createMutatingWebhook(ctx, c, clientConfig, rules, failurePolicy, sideEffects)
	case ValidatingWebhook:
		err = wm.createValidatingWebhook(ctx, c, clientConfig, rules, failurePolicy, sideEffects)
	default:
		err = fmt.Errorf("invalid webhook type: %s", wm.webhookType)
	}
	if err == nil {
		klog.Infof("Successfully applied webhook configuration for %s", webhookURL)
	}
	return err
}

func (wm *webhookManagerImpl[T]) createValidatingWebhook(ctx context.Context, c client.Client, clientConfig *admissionregistrationv1apply.WebhookClientConfigApplyConfiguration, rules []*admissionregistrationv1apply.RuleWithOperationsApplyConfiguration, failurePolicy admissionregistrationv1.FailurePolicyType, sideEffects admissionregistrationv1.SideEffectClass) error {
	applyConfig := admissionregistrationv1apply.ValidatingWebhookConfiguration(wm.config.Name).
		WithWebhooks(
			admissionregistrationv1apply.ValidatingWebhook().
				WithName(wm.config.WebhookName).
				WithClientConfig(clientConfig).
				WithRules(rules...).
				WithFailurePolicy(failurePolicy).
				WithSideEffects(sideEffects).
				WithAdmissionReviewVersions("v1", "v1beta1").
				WithObjectSelector(wm.config.ObjectSelector),
		)

	klog.Infof("Applying webhook configuration %s...", wm.config.Name)
	if err := c.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner("rdd-webhook-manager")); err != nil {
		klog.Errorf("Failed to apply webhook configuration: %v", err)
		return fmt.Errorf("failed to apply webhook configuration: %w", err)
	}
	return nil
}

func (wm *webhookManagerImpl[T]) createMutatingWebhook(ctx context.Context, c client.Client, clientConfig *admissionregistrationv1apply.WebhookClientConfigApplyConfiguration, rules []*admissionregistrationv1apply.RuleWithOperationsApplyConfiguration, failurePolicy admissionregistrationv1.FailurePolicyType, sideEffects admissionregistrationv1.SideEffectClass) error {
	applyConfig := admissionregistrationv1apply.MutatingWebhookConfiguration(wm.config.Name).
		WithWebhooks(
			admissionregistrationv1apply.MutatingWebhook().
				WithName(wm.config.WebhookName).
				WithClientConfig(clientConfig).
				WithRules(rules...).
				WithFailurePolicy(failurePolicy).
				WithSideEffects(sideEffects).
				WithAdmissionReviewVersions("v1", "v1beta1").
				WithObjectSelector(wm.config.ObjectSelector),
		)

	klog.Infof("Applying webhook configuration %s...", wm.config.Name)
	if err := c.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner("rdd-webhook-manager")); err != nil {
		klog.Errorf("Failed to apply webhook configuration: %v", err)
		return fmt.Errorf("failed to apply webhook configuration: %w", err)
	}
	return nil
}
