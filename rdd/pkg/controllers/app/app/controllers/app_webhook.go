// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"

	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// AppValidator validates App resources via admission webhook.
type AppValidator struct {
	supportedK8sVersions map[string]struct{}
}

// NewAppValidator parses k3sVersionsData once at construction time so that a
// malformed JSON fixture causes controller startup to fail rather than the
// first admission request.
func NewAppValidator(k3sVersionsData string) (*AppValidator, error) {
	supported, err := parseK3sVersions(k3sVersionsData)
	if err != nil {
		return nil, fmt.Errorf("failed to load supported Kubernetes versions: %w", err)
	}
	return &AppValidator{supportedK8sVersions: supported}, nil
}

// ValidateCreate validates a new App resource.
func (v *AppValidator) ValidateCreate(_ context.Context, obj *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(obj)
}

// ValidateUpdate validates an updated App resource.
func (v *AppValidator) ValidateUpdate(_ context.Context, _, newObj *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(newObj)
}

// ValidateDelete is a no-op; App deletion is governed by the cleanup finalizer.
func (v *AppValidator) ValidateDelete(_ context.Context, _ *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return nil, nil
}

func (v *AppValidator) validate(app *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	k8s := app.Spec.Kubernetes
	if k8s.Enabled && k8s.Version == "" {
		return nil, errors.New("spec.kubernetes.version must not be empty when spec.kubernetes.enabled is true")
	}

	if k8s.Version != "" {
		if _, ok := v.supportedK8sVersions[k8s.Version]; !ok {
			return nil, fmt.Errorf("spec.kubernetes.version %q is not supported; see the bundled k3s-versions.json for valid versions", k8s.Version)
		}
	}

	return nil, nil
}
