// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package limavm

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

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

// ValidateLimaVM validates a complete LimaVM object and returns warnings.
// Template validation is now handled by the ConfigMap admission webhook,
// so we only validate LimaVM-specific concerns here (like cross-namespace name uniqueness).
func ValidateLimaVM(ctx context.Context, c client.Client, limavm *v1alpha1.LimaVM) ([]string, error) {
	if limavm == nil {
		return nil, errors.New("limavm object cannot be nil")
	}

	var warnings []string

	// Skip validation if the object is being deleted
	// During deletion, the controller removes finalizers and may have already deleted owned resources
	if limavm.DeletionTimestamp != nil {
		return warnings, nil
	}

	if err := validateLimaVMUniqueName(ctx, c, limavm); err != nil {
		return warnings, err
	}

	return warnings, nil
}
