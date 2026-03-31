// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package container

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

type immutableValidator struct {
	Client client.Client
}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (c *immutableValidator) ValidateCreate(context.Context, *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, errors.New("webhook does not implement create")
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (c *immutableValidator) ValidateDelete(context.Context, *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, errors.New("webhook does not implement delete")
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (c *immutableValidator) ValidateUpdate(_ context.Context, oldContainer, newContainer *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	// Return an error if the old object does not match the new object.
	if !equality.Semantic.DeepEqual(oldContainer.Spec, newContainer.Spec) {
		return nil, fmt.Errorf("container objects must not be modified: old: %v, new: %v", oldContainer, newContainer)
	}

	return nil, nil
}
