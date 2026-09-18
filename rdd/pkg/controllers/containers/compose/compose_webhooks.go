// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/containerd/containerd/v2/pkg/identifiers"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/api"
)

// composeUpRequestValidator enforces the invariant for ComposeUpRequest that
// cannot be expressed declaratively in the CRD schema: metadata.name must be
// calculated from spec.namespace and spec.name (matching the name of the
// ComposeProject object it will create or update).
//
// spec.namespace and spec.name are immutable, enforced via CEL.  The other
// parts of the spec are mutable, and do require validation.
type composeUpRequestValidator struct {
	// Client is the cached API client; this is preferred.
	client.Client
	// Reader is an uncached API reader; this is needed to validate things before
	// the cache is ready.
	client.Reader
}

// ValidateCreate implements [ctrlwebhookadmission.Validator].
func (v *composeUpRequestValidator) ValidateCreate(ctx context.Context, request *v1alpha1.ComposeUpRequest) (ctrlwebhookadmission.Warnings, error) {
	var errs []error

	if err := identifiers.Validate(request.Spec.Namespace); err != nil {
		errs = append(errs, fmt.Errorf("invalid namespace %q: %w", request.Spec.Namespace, err))
	} else {
		// Check that the namespace exists.
		var namespace v1alpha1.ContainerNamespace
		key := client.ObjectKey{
			Namespace: request.ObjectMeta.Namespace,
			Name:      api.MirrorName("cns", request.Spec.Namespace),
		}
		err := v.Client.Get(ctx, key, &namespace)
		if _, isErrCacheNotStarted := errors.AsType[*cache.ErrCacheNotStarted](err); isErrCacheNotStarted {
			err = v.Reader.Get(ctx, key, &namespace)
		}
		if apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("namespace %q does not exist", request.Spec.Namespace))
		} else if err != nil {
			errs = append(errs, fmt.Errorf("failed to get namespace %q: %w", request.Spec.Namespace, err))
		}
	}

	if request.Spec.Name == "" || loader.NormalizeProjectName(request.Spec.Name) != request.Spec.Name {
		errs = append(errs, loader.InvalidProjectNameErr(request.Spec.Name))
	}

	errs = append(errs, v.validateSpec(ctx, request)...)

	// Check that the name is correct.
	expectedName := composeMirrorName(request.Spec.Namespace, request.Spec.Name)
	if request.ObjectMeta.Name != expectedName {
		errs = append(errs, fmt.Errorf(
			"metadata.name must be %q (derived from spec.namespace and spec.name), got %q",
			expectedName, request.ObjectMeta.Name))
	}

	return nil, errors.Join(errs...)
}

// ValidateUpdate implements [ctrlwebhookadmission.Validator].
func (v *composeUpRequestValidator) ValidateUpdate(ctx context.Context, oldRequest, newRequest *v1alpha1.ComposeUpRequest) (ctrlwebhookadmission.Warnings, error) {
	// metadata.name is immutable on update (it is only ever set on create), and
	// spec.namespace and spec.name are enforced via CEL; we only need to validate
	// spec.workingDir and spec.configs here.
	// We only validate it the spec has changed; otherwise a filesystem change
	// could block unrelated updates.
	changed := oldRequest.Spec.WorkingDir != newRequest.Spec.WorkingDir ||
		!slices.Equal(oldRequest.Spec.Configs, newRequest.Spec.Configs)
	var errs []error
	if changed {
		errs = v.validateSpec(ctx, newRequest)
	}

	return nil, errors.Join(errs...)
}

// ValidateDelete implements [ctrlwebhookadmission.Validator].
func (v *composeUpRequestValidator) ValidateDelete(_ context.Context, _ *v1alpha1.ComposeUpRequest) (ctrlwebhookadmission.Warnings, error) {
	// We do not do any validation on delete.
	return nil, nil
}

// validateSpec checks the mutable fields of the ComposeUpRequest spec.
func (v *composeUpRequestValidator) validateSpec(_ context.Context, request *v1alpha1.ComposeUpRequest) []error {
	var errs []error

	if request.Spec.WorkingDir == "" {
		errs = append(errs, errors.New("spec.workingDir must not be empty"))
	} else if !filepath.IsAbs(request.Spec.WorkingDir) {
		errs = append(errs, fmt.Errorf("spec.workingDir %q must be an absolute path", request.Spec.WorkingDir))
	} else if root, err := os.OpenRoot(request.Spec.WorkingDir); err != nil {
		errs = append(errs, fmt.Errorf("spec.workingDir %q cannot be opened: %w", request.Spec.WorkingDir, err))
	} else {
		defer root.Close()
		for _, config := range request.Spec.Configs {
			if config == "" {
				errs = append(errs, errors.New("spec.configs path must not be empty"))
			} else if stat, err := root.Stat(config); err != nil {
				errs = append(errs, fmt.Errorf("spec.configs path %q cannot be accessed: %w", config, err))
			} else if !stat.Mode().IsRegular() {
				errs = append(errs, fmt.Errorf("spec.configs path %q is not a regular file", config))
			}
		}
	}

	return errs
}

// ComposeProject objects are, by convention, only ever created/updated by
// this package's own reconciler; this is not enforced by a validator, since
// every local client (including the reconciler itself) authenticates with
// the same admin-equivalent identity on this single-user desktop, so a
// group-based identity check would not distinguish the two.
