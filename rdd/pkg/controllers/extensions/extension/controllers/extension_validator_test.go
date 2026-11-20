// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

func newValidatorTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, v1alpha1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func TestExtensionValidatorValidateCreate(t *testing.T) {
	t.Run("valid resource", func(t *testing.T) {
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t)}
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{Name: "ext"},
			Spec:       v1alpha1.ExtensionSpec{Image: "example.com/image"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.NilError(t, err)
		assert.Assert(t, warnings == nil)
	})

	t.Run("invalid spec image", func(t *testing.T) {
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t)}
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{Name: "anything"},
			Spec:       v1alpha1.ExtensionSpec{Image: "Invalid-Image"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.ErrorContains(t, err, "invalid spec.image")
		assert.Assert(t, warnings == nil)
	})

	t.Run("duplicate image", func(t *testing.T) {
		existing := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "existing-extension",
				Namespace: "default",
				UID:       "existing-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/dupe-image"},
		}
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t, existing)}
		obj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "new-extension",
				Namespace: "default",
				UID:       "new-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/dupe-image"},
		}

		warnings, err := validator.ValidateCreate(t.Context(), obj)
		assert.ErrorContains(t, err, `spec.image "example.com/dupe-image" is already used by another extension "existing-extension"`)
		assert.Assert(t, warnings == nil)
	})
}

func TestExtensionValidatorValidateUpdate(t *testing.T) {
	t.Run("valid update", func(t *testing.T) {
		existing := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-extension",
				Namespace: "default",
				UID:       "my-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/image:v1"},
		}
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t, existing)}

		oldObj := existing.DeepCopy()
		newObj := existing.DeepCopy()
		// Updating the object with the same image reference should succeed and not
		// collide with itself.
		newObj.ObjectMeta.Labels = map[string]string{"updated": "true"}

		warnings, err := validator.ValidateUpdate(t.Context(), oldObj, newObj)
		assert.NilError(t, err)
		assert.Assert(t, warnings == nil)
	})

	t.Run("invalid spec image", func(t *testing.T) {
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t)}
		oldObj := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-extension",
				Namespace: "default",
				UID:       "my-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/image:v1"},
		}
		newObj := oldObj.DeepCopy()
		newObj.Spec.Image = "Invalid-Image"

		warnings, err := validator.ValidateUpdate(t.Context(), oldObj, newObj)
		assert.ErrorContains(t, err, "invalid spec.image")
		assert.Assert(t, warnings == nil)
	})

	t.Run("duplicate image", func(t *testing.T) {
		existing := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-extension",
				Namespace: "default",
				UID:       "other-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/dupe-image"},
		}
		target := &v1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-extension",
				Namespace: "default",
				UID:       "my-uid",
			},
			Spec: v1alpha1.ExtensionSpec{Image: "example.com/original-image"},
		}
		validator := &ExtensionValidator{Reader: newValidatorTestClient(t, existing, target)}

		oldObj := target.DeepCopy()
		newObj := target.DeepCopy()
		newObj.Spec.Image = "example.com/dupe-image"

		warnings, err := validator.ValidateUpdate(t.Context(), oldObj, newObj)
		assert.ErrorContains(t, err, `spec.image "example.com/dupe-image" is already used by another extension "other-extension"`)
		assert.Assert(t, warnings == nil)
	})
}
