// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"

	"github.com/distribution/reference"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// ExtensionValidator validates Extension resources via admission webhook.
type ExtensionValidator struct {
	client.Reader
}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (v *ExtensionValidator) ValidateCreate(ctx context.Context, obj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(ctx, obj)
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (v *ExtensionValidator) ValidateUpdate(ctx context.Context, oldObj, newObj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	warnings, err := v.validate(ctx, newObj)
	errs := []error{err}
	oldTarget, oldErr := reference.ParseNormalizedNamed(oldObj.Spec.Image)
	newTarget, newErr := reference.ParseNormalizedNamed(newObj.Spec.Image)
	// If old or new spec fails to parse, v.validate would take care of it.
	if oldErr == nil && newErr == nil {
		if reference.FamiliarName(oldTarget) != reference.FamiliarName(newTarget) {
			errs = append(errs, fmt.Errorf("cannot change spec.image from %q to %q", oldTarget, newTarget))
		}
	}
	return warnings, errors.Join(errs...)
}

// ValidateDelete implements [ctrlwebhookadmission.Validator]; deletion is
// always allowed.
func (v *ExtensionValidator) ValidateDelete(_ context.Context, _ *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	return nil, nil
}

func (v *ExtensionValidator) validate(ctx context.Context, obj *v1alpha1.Extension) (ctrlwebhookadmission.Warnings, error) {
	ref, err := reference.ParseNormalizedNamed(obj.Spec.Image)
	if err != nil {
		return nil, fmt.Errorf("invalid spec.image: %w", err)
	}
	// targetName is the familiar name of the target image reference.
	targetName := reference.FamiliarName(ref)
	var list v1alpha1.ExtensionList
	err = v.List(ctx, &list, client.InNamespace(obj.Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list extensions: %w", err)
	}
	for _, ext := range list.Items {
		if ext.GetNamespace() == obj.GetNamespace() && ext.GetName() == obj.GetName() {
			continue
		}
		ref, err := reference.ParseNormalizedNamed(ext.Spec.Image)
		if err != nil {
			continue // Invalid spec on a _different_ image; not an issue with this.
		}
		if reference.FamiliarName(ref) == targetName {
			return nil, fmt.Errorf("spec.image %q is already used by another extension %q", ext.Spec.Image, ext.Name)
		}
	}
	return nil, nil
}

var _ ctrlwebhookadmission.Validator[*v1alpha1.Extension] = &ExtensionValidator{}
