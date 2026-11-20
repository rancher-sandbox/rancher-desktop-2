// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"crypto/sha1"
	"fmt"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	extensionsv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

func newInstalledReconcilerTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, containersv1alpha1.AddToScheme(scheme))
	assert.NilError(t, extensionsv1alpha1.AddToScheme(scheme))

	var exts []*extensionsv1alpha1.Extension
	for _, obj := range objs {
		if ext, ok := obj.(*extensionsv1alpha1.Extension); ok {
			if ext.UID == "" {
				ext.UID = types.UID("uid-" + ext.Name)
			}
			exts = append(exts, ext)
		}
	}
	for _, obj := range objs {
		if pull, ok := obj.(*containersv1alpha1.ImagePullRequest); ok && len(pull.OwnerReferences) == 0 {
			for _, ext := range exts {
				if pull.Labels[imagePullRequestExtensionLabel] == ext.Name {
					_ = ctrl.SetControllerReference(ext, pull, scheme)
					break
				}
			}
		}
	}

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithIndex(&containersv1alpha1.Image{}, ".status.repoTag", func(obj client.Object) []string {
			if image, ok := obj.(*containersv1alpha1.Image); ok && image.Status.RepoTag != "" {
				return []string{image.Status.RepoTag}
			}
			return nil
		})
	for _, ext := range exts {
		builder = builder.WithStatusSubresource(ext)
	}

	return builder.Build()
}

func TestExtensionReconcilerDownloadCreatesImagePullRequestWhenMissing(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
			UID:       types.UID("ext-uid"),
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	c := newInstalledReconcilerTestClient(t, ext)
	r := &ExtensionReconciler{Client: c}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	var pulls containersv1alpha1.ImagePullRequestList
	assert.NilError(t, c.List(t.Context(), &pulls, client.InNamespace(ext.Namespace)))
	assert.Equal(t, len(pulls.Items), 1)

	pull := pulls.Items[0]
	assert.Equal(t, pull.Labels[imagePullRequestExtensionLabel], ext.Name)
	assert.Equal(t, pull.Spec.Namespace, extensionNamespace)
	assert.Equal(t, pull.Spec.RepoTag, ext.Status.Image)
	assert.Equal(t, len(pull.OwnerReferences), 1)
	assert.Equal(t, pull.OwnerReferences[0].Name, ext.Name)
}

func TestExtensionReconcilerDownloadWaitsWhilePullInProgress(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	pull := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-1",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Settled",
					Status: metav1.ConditionFalse,
					Reason: "Copying",
				},
			},
		},
	}
	c := newInstalledReconcilerTestClient(t, ext, pull)
	r := &ExtensionReconciler{Client: c}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	assert.Assert(t, apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionInstalled) == nil)
}

func TestExtensionReconcilerDownloadSetsExtractingWhenPullFinished(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	pull := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-1",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Settled",
					Status: metav1.ConditionTrue,
					Reason: "Finished",
				},
			},
		},
	}
	image := &containersv1alpha1.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-image",
			Namespace: ext.Namespace,
		},
		Status: containersv1alpha1.ImageStatus{
			RepoTag: ext.Status.Image,
		},
	}
	c := newInstalledReconcilerTestClient(t, ext, pull, image)
	r := &ExtensionReconciler{Client: c}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	installed := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionInstalled)
	assert.Assert(t, installed != nil)
	assert.Equal(t, installed.Status, metav1.ConditionFalse)
	assert.Equal(t, installed.Reason, extensionsv1alpha1.ExtensionInstalledReasonExtracting)
	assert.Equal(t, installed.Message, "Extension image successfully downloaded")
}

func TestExtensionReconcilerDownloadPullFinishedWithoutImageRetries(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	pull := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-1",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Settled",
					Status: metav1.ConditionTrue,
					Reason: "Finished",
				},
			},
		},
	}
	c := newInstalledReconcilerTestClient(t, ext, pull)
	r := &ExtensionReconciler{Client: c}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	var pulls containersv1alpha1.ImagePullRequestList
	assert.NilError(t, c.List(
		t.Context(), &pulls,
		client.InNamespace(ext.Namespace),
		client.MatchingLabels{imagePullRequestExtensionLabel: ext.Name},
	))
	assert.Equal(t, len(pulls.Items), 1)
	assert.Assert(t, pulls.Items[0].Name != pull.Name)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	assert.Assert(t, apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionInstalled) == nil)
}

func TestExtensionReconcilerDownloadSetsDownloadFailedWhenPullFails(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	pull := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-1",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:    "Settled",
					Status:  metav1.ConditionTrue,
					Reason:  "Errored",
					Message: "registry request failed",
				},
			},
		},
	}
	c := newInstalledReconcilerTestClient(t, ext, pull)
	r := &ExtensionReconciler{Client: c, EventRecorder: &events.FakeRecorder{}}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	updated := &extensionsv1alpha1.Extension{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(ext), updated))
	installed := apimeta.FindStatusCondition(updated.Status.Conditions, extensionsv1alpha1.ExtensionConditionInstalled)
	assert.Assert(t, installed != nil)
	assert.Equal(t, installed.Status, metav1.ConditionFalse)
	assert.Equal(t, installed.Reason, extensionsv1alpha1.ExtensionInstalledReasonFailed)
	assert.Equal(t, installed.Message, "Failed to download extension image: registry request failed")
}

func TestExtensionReconcilerDownloadDeletesDuplicatePullRequests(t *testing.T) {
	ext := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension",
			Namespace: "rancher-desktop",
		},
		Status: extensionsv1alpha1.ExtensionStatus{
			Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
		},
	}
	pull1 := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-1",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Settled",
					Status: metav1.ConditionFalse,
					Reason: "Copying",
				},
			},
		},
	}
	pull2 := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-extension-pull-2",
			Namespace: ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Settled",
					Status: metav1.ConditionFalse,
					Reason: "Copying",
				},
			},
		},
	}
	c := newInstalledReconcilerTestClient(t, ext, pull1, pull2)
	r := &ExtensionReconciler{Client: c}

	err := r.download(t.Context(), ext)
	assertNoErrorOrRequeue(t, err)

	var pulls containersv1alpha1.ImagePullRequestList
	assert.NilError(t, c.List(
		t.Context(), &pulls,
		client.InNamespace(ext.Namespace),
		client.MatchingLabels{imagePullRequestExtensionLabel: ext.Name},
	))
	assert.Equal(t, len(pulls.Items), 1)
}

func TestExtensionReconcilerCreateImagePullRequest(t *testing.T) {
	t.Run("happy case", func(t *testing.T) {
		ext := &extensionsv1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-extension",
				Namespace: "rancher-desktop",
				UID:       types.UID("ext-uid"),
			},
			Status: extensionsv1alpha1.ExtensionStatus{
				Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
			},
		}
		c := newInstalledReconcilerTestClient(t, ext)
		r := &ExtensionReconciler{Client: c}

		assertNoErrorOrRequeue(t, r.createImagePullRequest(t.Context(), ext))

		var pulls containersv1alpha1.ImagePullRequestList
		assert.NilError(t, c.List(t.Context(), &pulls, client.InNamespace(ext.Namespace)))
		assert.Equal(t, len(pulls.Items), 1)

		pull := pulls.Items[0]
		assert.Assert(t, strings.HasPrefix(pull.Name, ext.Name+"-pull-"))
		assert.Equal(t, pull.Labels[imagePullRequestExtensionLabel], ext.Name)
		assert.Equal(t, pull.Spec.Namespace, extensionNamespace)
		assert.Equal(t, pull.Spec.RepoTag, ext.Status.Image)
		assert.Equal(t, len(pull.OwnerReferences), 1)
		assert.Equal(t, pull.OwnerReferences[0].Name, ext.Name)
	})

	t.Run("uses short prefix when extension name is too long", func(t *testing.T) {
		longName := "this-extension-name-is-deliberately-very-long-to-trigger-a-short-generated-prefix"
		ext := &extensionsv1alpha1.Extension{
			ObjectMeta: metav1.ObjectMeta{
				Name:      longName,
				Namespace: "rancher-desktop",
				UID:       types.UID("ext-uid"),
			},
			Status: extensionsv1alpha1.ExtensionStatus{
				Image: "ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest",
			},
		}
		c := newInstalledReconcilerTestClient(t, ext)
		r := &ExtensionReconciler{Client: c}

		assertNoErrorOrRequeue(t, r.createImagePullRequest(t.Context(), ext))

		var pulls containersv1alpha1.ImagePullRequestList
		assert.NilError(t, c.List(t.Context(), &pulls, client.InNamespace(ext.Namespace)))
		assert.Equal(t, len(pulls.Items), 1)

		expectedPrefix := fmt.Sprintf("ext-%02x-pull-", sha1.Sum([]byte(longName)))
		assert.Assert(t, strings.HasPrefix(pulls.Items[0].Name, expectedPrefix))
	})
}
