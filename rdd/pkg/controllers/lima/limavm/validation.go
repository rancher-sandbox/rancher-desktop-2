// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package limavm

import (
	"context"
	"errors"
	"fmt"

	"github.com/lima-vm/lima/v2/pkg/limayaml"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
)

// validateLimaVMUniqueName validates that the LimaVM name is unique across all namespaces.
// This is critical because LimaVM names correspond to actual VM instances on the host system,
// which must be unique.
func validateLimaVMUniqueName(ctx context.Context, c client.Client, limavm *v1alpha1.LimaVM) error {
	// List all LimaVMs across all namespaces
	limavmList := &v1alpha1.LimaVMList{}
	if err := c.List(ctx, limavmList, &client.ListOptions{}); err != nil {
		return fmt.Errorf("failed to list LimaVMs for uniqueness check: %w", err)
	}

	// Check for name conflicts across all namespaces
	for _, existingVM := range limavmList.Items {
		// Skip if it's the same resource (UPDATE operation)
		if existingVM.UID == limavm.UID {
			continue
		}

		// Check if another instance with the same name exists
		if existingVM.Name == limavm.Name {
			return fmt.Errorf("LimaVM name %q is already used in namespace %q; LimaVM names must be unique across all namespaces",
				limavm.Name, existingVM.Namespace)
		}
	}

	return nil
}

// validateLimaVM validates a complete LimaVM object and returns warnings.
// Template validation is now handled by the ConfigMap admission webhook,
// so we only validate LimaVM-specific concerns here (like cross-namespace name uniqueness).
func validateLimaVM(ctx context.Context, c client.Client, limavm *v1alpha1.LimaVM) ([]string, error) {
	if limavm == nil {
		return nil, errors.New("limavm object cannot be nil")
	}

	var warnings []string

	// Skip validation if the object is being deleted
	// During deletion, the controller removes finalizers and may have already deleted owned resources
	if !limavm.DeletionTimestamp.IsZero() {
		return warnings, nil
	}

	if err := validateLimaVMUniqueName(ctx, c, limavm); err != nil {
		return warnings, err
	}

	return warnings, nil
}

// validateTemplateData validates the template data map from a ConfigMap.
func validateTemplateData(ctx context.Context, data map[string]string) (ctrlwebhookadmission.Warnings, error) {
	templateData, exists := data[v1alpha1.TemplateConfigMapKey]
	if !exists {
		return nil, fmt.Errorf("template ConfigMap must have a %q data entry", v1alpha1.TemplateConfigMapKey)
	}
	if templateData == "" {
		return nil, fmt.Errorf("%q data cannot be empty", v1alpha1.TemplateConfigMapKey)
	}

	// Call the Lima validator
	y, err := limayaml.Load(ctx, []byte(templateData), "")
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	// Second parameter controls whether to log warnings via logrus.Warn
	if err := limayaml.Validate(y, false); err != nil {
		return nil, fmt.Errorf("failed to validate template: %w", err)
	}

	return nil, nil
}
