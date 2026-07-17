// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// K3sVersionsValidator is a validating webhook that prevents the k3s-versions
// config map from being deleted.
type K3sVersionsValidator struct{}

// ValidateCreate implements [admission.Validator].
func (k *K3sVersionsValidator) ValidateCreate(context.Context, *v1.ConfigMap) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements [admission.Validator].
func (k *K3sVersionsValidator) ValidateUpdate(context.Context, *v1.ConfigMap, *v1.ConfigMap) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements [admission.Validator].
func (k *K3sVersionsValidator) ValidateDelete(ctx context.Context, obj *v1.ConfigMap) (admission.Warnings, error) {
	log.FromContext(ctx).Info("Checking for config map deletion",
		"name", obj.GetName(),
		"namespace", obj.GetNamespace())
	if obj.GetNamespace() != controllers.RDDSystemNamespace || obj.GetName() != k3sVersionsConfigMapName {
		// This is not our config map.
		return nil, nil
	}
	// We reject all deletes.
	return nil, apierrors.NewForbidden(
		v1.SchemeGroupVersion.WithResource("ConfigMap").GroupResource(),
		obj.Name,
		fmt.Errorf("refusing to delete %T %s/%s", obj, obj.GetNamespace(), obj.GetName()),
	)
}

var _ admission.Validator[*v1.ConfigMap] = &K3sVersionsValidator{}
