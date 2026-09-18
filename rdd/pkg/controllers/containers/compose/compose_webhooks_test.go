// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// newValidComposeUpRequest returns a ComposeUpRequest whose metadata.name
// matches the deterministic name computed from namespace/name, so that only
// the specific field under test needs to be overridden by the caller.
func newValidComposeUpRequest(namespace, name, kubename, tempDir string) *v1alpha1.ComposeUpRequest {
	return &v1alpha1.ComposeUpRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      kubename,
			Namespace: "rancher-desktop",
		},
		Spec: v1alpha1.ComposeUpRequestSpec{
			Namespace:  namespace,
			Name:       name,
			WorkingDir: tempDir,
		},
	}
}

func TestComposeUpRequestValidator_ValidateCreate(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	assert.NilError(t, v1alpha1.AddToScheme(scheme))

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&v1alpha1.ContainerNamespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "moby",
				Namespace: "rancher-desktop",
			},
		}).
		Build()

	v := &composeUpRequestValidator{Client: cl}

	t.Run("accepts a correctly-named request", func(t *testing.T) {
		t.Parallel()
		request := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		_, err := v.ValidateCreate(t.Context(), request)
		assert.NilError(t, err)
	})

	t.Run("accepts a hashed name where required", func(t *testing.T) {
		t.Parallel()
		request := newValidComposeUpRequest("moby", "myproject-",
			"cmp-5807cf0f982818547a7d093ccf9fc69c7a4eae1244e26105e30ccdb9ca985399", t.TempDir())
		_, err := v.ValidateCreate(t.Context(), request)
		assert.NilError(t, err)
	})

	t.Run("rejects a request whose metadata.name does not match the computed hash", func(t *testing.T) {
		t.Parallel()
		request := newValidComposeUpRequest("moby", "myproject-",
			"cmp-0000000000000000000000000000000000000000000000000000000000000000", t.TempDir())
		_, err := v.ValidateCreate(t.Context(), request)
		assert.ErrorContains(t, err, "metadata.name must be")
	})
}

func TestComposeUpRequestValidator_ValidateUpdate(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	assert.NilError(t, v1alpha1.AddToScheme(scheme))

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&v1alpha1.ContainerNamespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "moby",
			Namespace: "rancher-desktop",
		},
	}).Build()
	v := &composeUpRequestValidator{Client: cl}

	t.Run("accepts a valid update", func(t *testing.T) {
		t.Parallel()
		oldReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		newReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		_, err := v.ValidateUpdate(t.Context(), oldReq, newReq)
		assert.NilError(t, err)
	})

	simpleCases := map[string]*v1alpha1.ComposeUpRequest{
		"workingDir must not be empty": newValidComposeUpRequest("moby", "myproject", "moby.myproject", ""),
		"must be an absolute path":     newValidComposeUpRequest("moby", "myproject", "moby.myproject", "./relative"),
	}
	for errorText, newReq := range simpleCases {
		t.Run(errorText, func(t *testing.T) {
			t.Parallel()
			oldReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
			_, err := v.ValidateUpdate(t.Context(), oldReq, newReq)
			assert.ErrorContains(t, err, errorText)
		})
	}

	t.Run("rejects an update with non-existent workingDir", func(t *testing.T) {
		t.Parallel()
		oldReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		newReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", filepath.Join(t.TempDir(), "not-found"))
		_, err := v.ValidateUpdate(t.Context(), oldReq, newReq)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	})

	t.Run("rejects an update with configs outside workingDir", func(t *testing.T) {
		t.Parallel()
		oldReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		newReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		// Simulate a config outside the workingDir
		newReq.Spec.Configs = []string{
			filepath.Join(t.TempDir(), "outside"),
		}
		_, err := v.ValidateUpdate(t.Context(), oldReq, newReq)
		// This is [os.errPathEscapes], which is not exported.
		assert.ErrorContains(t, err, "path escapes from parent")
	})

	t.Run("rejects an update where workingDir is a file", func(t *testing.T) {
		t.Parallel()
		oldReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", t.TempDir())
		file := filepath.Join(t.TempDir(), "file")
		assert.NilError(t, os.WriteFile(file, []byte("content"), 0o644))
		newReq := newValidComposeUpRequest("moby", "myproject", "moby.myproject", file)
		_, err := v.ValidateUpdate(t.Context(), oldReq, newReq)
		assert.ErrorContains(t, err, "not a directory")
	})
}
