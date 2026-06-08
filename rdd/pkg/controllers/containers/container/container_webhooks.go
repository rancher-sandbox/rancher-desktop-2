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
// The engine watcher creates Container mirrors and never sets the action
// annotation; reject any create that carries one so a hand-written
// Container cannot drive a Docker action against its metadata.name.
func (c *immutableValidator) ValidateCreate(_ context.Context, newContainer *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	if _, ok := newContainer.Annotations[v1alpha1.AnnotationAction]; ok {
		return nil, fmt.Errorf("%s annotation is not allowed on create", v1alpha1.AnnotationAction)
	}
	return nil, nil
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (c *immutableValidator) ValidateDelete(context.Context, *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	return nil, errors.New("webhook does not implement delete")
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
// The whole Container spec is immutable on update: actions are requested via
// the AnnotationAction annotation on metadata, not via a level-triggered
// desired-state field.
//
// Provenance of the annotation is not checked. ValidateCreate rejects
// creates that carry the action annotation, but a caller can create an
// empty Container and then PATCH the annotation in. Closing that bypass
// would require a managedFields check, which is more cost than benefit on
// a single-user desktop where the principal already has Docker socket
// access.
func (c *immutableValidator) ValidateUpdate(_ context.Context, oldContainer, newContainer *v1alpha1.Container) (warnings ctrlwebhookadmission.Warnings, err error) {
	if !equality.Semantic.DeepEqual(oldContainer.Spec, newContainer.Spec) {
		return nil, fmt.Errorf("spec is immutable: old: %v, new: %v", oldContainer.Spec, newContainer.Spec)
	}
	if raw, ok := newContainer.Annotations[v1alpha1.AnnotationAction]; ok {
		if !v1alpha1.ContainerAction(raw).IsValid() {
			return nil, fmt.Errorf("invalid %s annotation %q", v1alpha1.AnnotationAction, raw)
		}
	}
	return nil, nil
}
