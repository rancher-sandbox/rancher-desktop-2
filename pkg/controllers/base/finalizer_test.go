// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package base

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestDeleteOwnedResources(t *testing.T) {
	env := &envtest.Environment{
		DownloadBinaryAssets: true,
	}
	cfg, err := env.Start()
	assert.NilError(t, err, "failed to start environment")

	defer func() {
		err := env.Stop()
		// On Windows, `env.Stop()` will return an error because it can't kill
		// etcd gracefully; this is not an issue for this test.
		// Also, in CI only, ignore failure to stop kube-apiserver.
		if runtime.GOOS != "windows" && err != nil {
			checkError := os.Getenv("CI") == ""
			checkError = checkError || !strings.Contains(err.Error(), "timeout waiting for process kube-apiserver to stop")
			if checkError {
				assert.NilError(t, err, "failed to stop environment")
			}
		}
	}()

	c, err := client.New(cfg, client.Options{})
	assert.NilError(t, err, "failed to create client")
	assert.Assert(t, c != nil, "got nil client")

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{})
	assert.NilError(t, err, "failed to create manager")
	assert.Assert(t, mgr != nil, "got nil manager")

	ctrllog.SetLogger(klog.NewKlogr())

	testCases := []struct {
		name       string
		finalizers []string
		// expectDeleted is true when the owned resource should be fully deleted
		// (NotFound) after DeleteOwnedResources. When false, the resource still
		// exists because a remaining finalizer blocks deletion.
		expectDeleted bool
	}{
		{
			name:          "config map without finalizers",
			expectDeleted: true,
		},
		{
			name:          "config map with owned finalizer only",
			finalizers:    []string{OwnedFinalizerName},
			expectDeleted: true,
		},
		{
			name:          "config map with both finalizers strips only owned",
			finalizers:    []string{CleanupFinalizerName, OwnedFinalizerName},
			expectDeleted: false,
		},
	}

	for i, tc := range testCases {
		t.Run(fmt.Sprintf("delete owned config map %s", tc.name), func(t *testing.T) {
			// Create a new config map that will be the owner
			owner := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("owner-%d", i),
					Namespace: metav1.NamespaceDefault,
				},
			}
			assert.NilError(t, c.Create(t.Context(), owner), "failed to create owner config map")
			ownerSchemes, _, err := scheme.Scheme.ObjectKinds(owner)
			assert.NilError(t, err, "failed to get object kinds")

			// Create a new config map that will be owned by the owner
			owned := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("owned-%d", i),
					Namespace: metav1.NamespaceDefault,
				},
			}
			owned.SetOwnerReferences([]metav1.OwnerReference{
				{
					APIVersion: ownerSchemes[0].GroupVersion().String(),
					Kind:       ownerSchemes[0].Kind,
					Name:       owner.Name,
					UID:        owner.UID,
				},
			})
			owned.SetFinalizers(tc.finalizers)
			assert.NilError(t, c.Create(t.Context(), owned), "failed to create owned config map")

			unrelated := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("unrelated-%d", i),
					Namespace: metav1.NamespaceDefault,
				},
			}
			assert.NilError(t, c.Create(t.Context(), unrelated), "failed to create unrelated config map")
			unrelatedSchemes, _, err := scheme.Scheme.ObjectKinds(unrelated)
			assert.NilError(t, err, "failed to get object kinds")
			unrelated.SetOwnerReferences([]metav1.OwnerReference{{
				APIVersion: unrelatedSchemes[0].GroupVersion().String(),
				Kind:       unrelatedSchemes[0].Kind,
				Name:       unrelated.GetName(),
				UID:        unrelated.GetUID(),
			}})
			assert.NilError(t, c.Update(t.Context(), unrelated), "failed to update unrelated config map")

			// Delete owned resources
			err = DeleteOwnedResources(
				t.Context(),
				c,
				owner,
				mgr)
			assert.NilError(t, err, "failed to delete owned resources")

			err = c.Get(t.Context(), client.ObjectKeyFromObject(owned), owned)
			if tc.expectDeleted {
				assert.Check(t, apierrors.IsNotFound(err), "owned config map was not deleted: %s", err)
			} else {
				// Resource still exists because cleanup finalizer blocks deletion
				assert.NilError(t, err, "failed to get owned config map")
				assert.Check(t, !owned.GetDeletionTimestamp().IsZero(), "owned config map should be marked for deletion")
				assert.Check(t, HasCleanupFinalizer(owned), "cleanup finalizer should remain")
				assert.Check(t, !HasOwnedFinalizer(owned), "owned finalizer should be stripped")
			}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(owner), owner)
			assert.Check(t, cmp.ErrorIs(err, nil), "failed to get owner object")
			err = c.Get(t.Context(), client.ObjectKeyFromObject(unrelated), unrelated)
			assert.Check(t, cmp.ErrorIs(err, nil), "failed to get unrelated object")
		})
	}
}
