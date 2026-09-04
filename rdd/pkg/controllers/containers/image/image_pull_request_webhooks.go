// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package image

import (
	"context"
	"errors"
	"fmt"

	"github.com/distribution/reference"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

type imagePullRequestValidator struct {
	reader client.Reader
}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (v *imagePullRequestValidator) ValidateCreate(ctx context.Context, imagePullRequest *v1alpha1.ImagePullRequest) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(ctx, imagePullRequest)
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (v *imagePullRequestValidator) ValidateUpdate(ctx context.Context, oldImagePullRequest, newImagePullRequest *v1alpha1.ImagePullRequest) (ctrlwebhookadmission.Warnings, error) {
	if oldImagePullRequest.Spec.Namespace != newImagePullRequest.Spec.Namespace {
		return nil, errors.New("spec.namespace is immutable")
	}
	if oldImagePullRequest.Spec.RepoTag != newImagePullRequest.Spec.RepoTag {
		return nil, errors.New("spec.repoTag is immutable")
	}
	return v.validate(ctx, newImagePullRequest)
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (v *imagePullRequestValidator) ValidateDelete(_ context.Context, _ *v1alpha1.ImagePullRequest) (ctrlwebhookadmission.Warnings, error) {
	return nil, nil
}

func (v *imagePullRequestValidator) validate(ctx context.Context, imagePullRequest *v1alpha1.ImagePullRequest) (ctrlwebhookadmission.Warnings, error) {
	if imagePullRequest.Spec.Namespace != "" {
		key := types.NamespacedName{
			Namespace: imagePullRequest.Namespace,
			Name:      imagePullRequest.Spec.Namespace,
		}
		containerNamespace := &v1alpha1.ContainerNamespace{}
		if err := v.reader.Get(ctx, key, containerNamespace); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("spec.namespace %q does not refer to an existing ContainerNamespace in namespace %q", imagePullRequest.Spec.Namespace, imagePullRequest.Namespace)
			}
			return nil, fmt.Errorf("failed to validate spec.namespace %q in namespace %q: %w", imagePullRequest.Spec.Namespace, imagePullRequest.Namespace, err)
		}
	}

	if _, err := reference.ParseNormalizedNamed(imagePullRequest.Spec.RepoTag); err != nil {
		return nil, fmt.Errorf("invalid spec.repoTag: %w", err)
	}

	return nil, nil
}

var _ ctrlwebhookadmission.Validator[*v1alpha1.ImagePullRequest] = &imagePullRequestValidator{}
