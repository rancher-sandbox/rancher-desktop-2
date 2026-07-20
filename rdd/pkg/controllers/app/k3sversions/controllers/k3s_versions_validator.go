// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// K3sVersionsValidator is a validating webhook that prevents the k3s-versions
// config map from being deleted.
type K3sVersionsValidator struct {
	client.Client
}

// Get the app associated with the given config map.
func (k *K3sVersionsValidator) getApp(ctx context.Context, obj *v1.ConfigMap) (*v1alpha1.App, error) {
	if obj.GetName() != k3sVersionsConfigMapName {
		// This is not our config map.
		return nil, nil
	}
	labels := obj.GetLabels()
	for k, v := range desiredLabels {
		if labels[k] != v {
			// This config map does not have our labels; it should not have been passed to the webhook.
			return nil, nil
		}
	}

	ownerRef := metav1.GetControllerOf(obj)
	if ownerRef == nil || ownerRef.Kind != v1alpha1.AppKind || ownerRef.APIVersion != v1alpha1.GroupVersion.String() {
		// This config map isn't owned by our app.
		return nil, nil
	}
	app := &v1alpha1.App{}
	if err := k.Get(ctx, client.ObjectKey{Name: ownerRef.Name}, app); err != nil {
		if apierrors.IsNotFound(err) {
			// The app doesn't exist.  The reconciler should be deleting the config map.
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get app %s/%s: %w", obj.GetNamespace(), ownerRef.Name, err)
	}

	return app, nil
}

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
	app, err := k.getApp(ctx, obj)
	if err != nil {
		log.FromContext(ctx).V(1).Info("Failed to get associated app for config map",
			"name", obj.GetName(),
			"namespace", obj.GetNamespace(),
			"error", err)
		return nil, err
	}
	if app == nil {
		// No associated app; not our config map, or it needs to be deleted.
		log.FromContext(ctx).V(1).Info("Config map does not have associated app; allowing deletion",
			"name", obj.GetName(),
			"namespace", obj.GetNamespace())
		return nil, nil
	}

	if base.IsBeingDeleted(app) {
		// The app is being deleted; we can delete the config map.
		log.FromContext(ctx).V(1).Info("Config map has associated app that is being deleted; allowing delete",
			"name", obj.GetName(),
			"namespace", obj.GetNamespace(),
			"app", app.GetName())
		return nil, nil
	}

	// We reject all deletes.
	log.FromContext(ctx).V(1).Info("Config map has associated app that is not being deleted; rejecting delete",
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"app", app.GetName())
	return nil, apierrors.NewForbidden(
		v1.SchemeGroupVersion.WithResource("ConfigMap").GroupResource(),
		obj.Name,
		fmt.Errorf("refusing to delete %T %s/%s", obj, obj.GetNamespace(), obj.GetName()),
	)
}

var _ admission.Validator[*v1.ConfigMap] = &K3sVersionsValidator{}
