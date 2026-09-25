// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

func TestExtensionValidatorValidateCreate(t *testing.T) {
	validator := &ExtensionValidator{}

	t.Run("valid resource", func(t *testing.T) {
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{Name: "ghcr.io-rancher-sandbox-rancher-desktop-rdx-host-api-test"},
			Spec:       v1alpha1.ExtensionSpec{Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.NilError(t, err)
		assert.Assert(t, warnings == nil)
	})

	t.Run("invalid spec image", func(t *testing.T) {
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{Name: "anything"},
			Spec:       v1alpha1.ExtensionSpec{Image: "Invalid-Image"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.ErrorContains(t, err, "invalid spec.image")
		assert.Assert(t, warnings == nil)
	})

	t.Run("metadata name mismatch", func(t *testing.T) {
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{Name: "wrong-name"},
			Spec:       v1alpha1.ExtensionSpec{Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.ErrorContains(t, err, `metadata.name "wrong-name" does not match the sanitized version of spec.image`)
		assert.ErrorContains(t, err, `expected "ghcr.io-rancher-sandbox-rancher-desktop-rdx-host-api-test"`)
		assert.Assert(t, warnings == nil)
	})
}

func TestExtensionValidatorValidateUpdate(t *testing.T) {
	validator := &ExtensionValidator{}

	oldObj := &v1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{Name: "old-wrong-name"},
		Spec:       v1alpha1.ExtensionSpec{Image: "Invalid-Image"},
	}
	newObj := &v1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{Name: "hello-world"},
		Spec:       v1alpha1.ExtensionSpec{Image: "docker.io/library/hello-world:tag"},
	}

	warnings, err := validator.ValidateUpdate(t.Context(), oldObj, newObj)
	assert.NilError(t, err)
	assert.Assert(t, warnings == nil)
}

func TestExtensionValidatorValidateDelete(t *testing.T) {
	validator := &ExtensionValidator{}

	obj := &v1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{Name: "any-extension"},
		Spec:       v1alpha1.ExtensionSpec{Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test"},
	}

	warnings, err := validator.ValidateDelete(t.Context(), obj)
	assert.NilError(t, err)
	assert.Assert(t, warnings == nil)
}
