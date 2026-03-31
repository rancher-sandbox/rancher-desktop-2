// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package containernamespace

import (
	"context"
	"errors"

	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

type deleteValidator struct{}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (c *deleteValidator) ValidateCreate(context.Context, *v1alpha1.ContainerNamespace) (ctrlwebhookadmission.Warnings, error) {
	return nil, errors.New("webhook does not implement create")
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (c *deleteValidator) ValidateDelete(context.Context, *v1alpha1.ContainerNamespace) (ctrlwebhookadmission.Warnings, error) {
	// TODO: We should validate that:
	// The namespace to be deleted must be empty before allowing the delete.
	// Additionally, when using moby backend: we do not delete the `moby` namespace.
	return ctrlwebhookadmission.Warnings{"namespace delete validation not implemented"}, nil
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (c *deleteValidator) ValidateUpdate(context.Context, *v1alpha1.ContainerNamespace, *v1alpha1.ContainerNamespace) (ctrlwebhookadmission.Warnings, error) {
	return nil, errors.New("webhook does not implement update")
}
