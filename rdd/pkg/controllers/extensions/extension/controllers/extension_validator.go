// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"

	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// ExtensionValidator validates Extension resources via admission webhook.
type ExtensionValidator struct{}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (v *ExtensionValidator) ValidateCreate(_ context.Context, obj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(obj)
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (v *ExtensionValidator) ValidateUpdate(_ context.Context, _, newObj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(newObj)
}

// ValidateDelete implements [ctrlwebhookadmission.Validator]; deletion is
// always allowed.
func (v *ExtensionValidator) ValidateDelete(_ context.Context, _ *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	return nil, nil
}

func (v *ExtensionValidator) validate(obj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	expected, err := SanitizeImageName(obj.Spec.Image)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.image: %w", err)
	}
	if obj.Name != expected {
		return nil, fmt.Errorf("metadata.name %q does not match the sanitized version of spec.image; expected %q", obj.Name, expected)
	}
	return nil, nil
}

var _ ctrlwebhookadmission.Validator[*v1alpha1.Extension] = &ExtensionValidator{}
