// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"errors"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func (c *controller) setupWebhookWithManager(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Setting up Mock Namespace webhook")

	// set up the container controller with a webhook which prevents all modification.
	mutatingConfig := base.WebhookConfig[*corev1.Namespace]{
		Name:        "mock-namespace-no-delete",
		WebhookName: "mock-namespace-no-delete.mock.rancherdesktop.io",
		WebhookPort: c.webhookPort,
		Operations: []admissionregistrationv1.OperationType{
			admissionregistrationv1.Delete,
		},
		Validator: &staticNamespaceNoDeleteValidator{},
	}

	managers, err := base.SetupWebhookForResource(mgr, &corev1.Namespace{}, mutatingConfig)
	if err != nil {
		mgr.GetLogger().Error(err, "Failed to set up Mock Namespace webhook")
		return err
	}
	c.webhookManagers = append(c.webhookManagers, managers...)

	return nil
}

func (c *controller) GetWebhookServiceName() string {
	return controllerName + "-webhook"
}

func (c *controller) GetWebhookManagers() []base.WebhookManager {
	return c.webhookManagers
}

type staticNamespaceNoDeleteValidator struct{}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (c *staticNamespaceNoDeleteValidator) ValidateCreate(context.Context, *corev1.Namespace) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (c *staticNamespaceNoDeleteValidator) ValidateDelete(ctx context.Context, ns *corev1.Namespace) (warnings ctrlwebhookadmission.Warnings, err error) {
	if ns.Name == mockNamespaceName {
		klog.FromContext(ctx).V(4).Info("Rejecting delete of namespace")
		return nil, apierrors.NewForbidden(
			corev1.SchemeGroupVersion.WithResource("Namespace").GroupResource(),
			ns.Name,
			errors.New("deletion of mock namespace is not allowed"),
		)
	}
	return nil, nil
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (c *staticNamespaceNoDeleteValidator) ValidateUpdate(_ context.Context, _, _ *corev1.Namespace) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, nil
}
