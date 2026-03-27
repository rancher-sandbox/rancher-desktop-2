// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mobyimage "github.com/moby/moby/api/types/image"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

//go:embed testdata/images.json
var testImages []byte

type imageReconciler struct {
	client.Client
	Recorder events.EventRecorder
	inspects []mobyimage.InspectResponse
}

// sanitizeKubernetesObjectName replaces characters that are not allowed in
// Kubernetes object names.
func sanitizeKubernetesObjectName(input string) string {
	return strings.NewReplacer("/", "-", ":", ".").Replace(input)
}

// Reconcile implements [reconcile.TypedReconciler].
func (r *imageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var errs []error

	// Check for the CRD to be registered.
	const crdName = "images.containers.rancherdesktop.io"
	var crd apiextensionsv1.CustomResourceDefinition
	if err := r.Client.Get(ctx, types.NamespacedName{Name: crdName}, &crd); err != nil {
		log.Error(err, "Failed to get CRD", "crd", crdName)
		return ctrl.Result{}, err
	}

	var rddNamespace corev1.Namespace
	if err := r.Client.Get(ctx, req.NamespacedName, &rddNamespace); err != nil {
		log.Error(err, "Failed to get namespace", "namespace", req.NamespacedName)
		return ctrl.Result{}, err
	}
	gvk, err := r.Client.GroupVersionKindFor(&rddNamespace)
	if err != nil {
		log.Error(err, "Failed to get GVK for namespace", "namespace", &rddNamespace)
		return ctrl.Result{}, err
	}

	ownerReference := metav1apply.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(rddNamespace.GetName()).
		WithUID(rddNamespace.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true)

	for _, inspect := range r.inspects {
		statusApplyConfig := containersv1alpha1apply.ImageStatus().
			WithID(inspect.ID).
			WithRepoDigests(inspect.RepoDigests...).
			WithArchitecture(inspect.Architecture).
			WithOS(inspect.Os).
			WithSize(inspect.Size).
			WithLabels(inspect.Config.Labels)
		if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
			statusApplyConfig.WithCreatedAt(metav1.NewTime(t))
		} else if inspect.Created != "" {
			imageName := inspect.ID
			if len(inspect.RepoTags) > 0 {
				imageName = inspect.RepoTags[0]
			}
			log.Error(err, "Failed to parse image created time", "image", imageName, "created", inspect.Created)
		}

		if len(inspect.RepoTags) > 0 {
			for _, tag := range inspect.RepoTags {
				// Deterministically generate a name for the Image based on the
				// tag and the image ID, to avoid having to search for an
				// existing image since we cannot atomically update the status
				// at the same time as creating the object.
				name := fmt.Sprintf("%s-%x",
					sanitizeKubernetesObjectName(inspect.ID),
					sha256.Sum256([]byte(tag)))
				statusApplyCopy := *statusApplyConfig
				errs = append(errs, r.updateImage(ctx,
					containersv1alpha1apply.Image(name, apiNamespace).
						WithOwnerReferences(ownerReference),
					statusApplyCopy.
						WithRepoTag(tag).
						WithNamespace(containerNamespace),
				)...)
			}
		} else {
			// No tags; create a single dangling image.
			errs = append(errs, r.updateImage(ctx,
				containersv1alpha1apply.Image(sanitizeKubernetesObjectName(inspect.ID), apiNamespace).
					WithOwnerReferences(ownerReference),
				statusApplyConfig)...,
			)
		}
	}

	if len(errs) > 0 {
		log.V(9).Info("Reconciled with errors", "count", len(r.inspects), "errors", len(errs))
		return ctrl.Result{}, errors.Join(errs...)
	}

	return ctrl.Result{}, nil
}

// updateImage applies the given configuration to the Image and its status.
func (r *imageReconciler) updateImage(
	ctx context.Context,
	image *containersv1alpha1apply.ImageApplyConfiguration,
	status *containersv1alpha1apply.ImageStatusApplyConfiguration,
) []error {
	var errs []error
	err := r.Client.Apply(
		ctx,
		image,
		client.ForceOwnership,
		client.FieldOwner(controllerLongName))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to apply image %s: %w", *image.GetName(), err))
	}
	// Update the status subresource separately, per Kubernetes API requirements.
	err = r.Client.SubResource("status").Apply(
		ctx,
		containersv1alpha1apply.Image(*image.GetName(), *image.GetNamespace()).
			WithStatus(status),
		client.ForceOwnership,
		client.FieldOwner(controllerLongName))
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to apply image status %s: %w", *image.GetName(), err))
	}
	return errs
}

func (r *imageReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	var errs []error
	if err := base.IndexFields(ctx, &containersv1alpha1.Image{}, mgr); err != nil {
		errs = append(errs, err)
	}

	var inspects []mobyimage.InspectResponse
	if err := json.Unmarshal(testImages, &inspects); err != nil {
		errs = append(errs, fmt.Errorf("failed to load static test data: %w", err))
	}
	r.inspects = inspects

	err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-image-reconciler").
		Watches(
			&containersv1alpha1.Image{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(),
				mgr.GetRESTMapper(),
				&corev1.Namespace{},
				handler.OnlyControllerOwner(),
			)).
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if _, ok := object.(*corev1.Namespace); ok {
				return object.GetName() == mockNamespaceName
			}
			if _, ok := object.(*containersv1alpha1.Image); ok {
				return true
			}
			return false
		})).
		Complete(r)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to setup image controller: %w", err))
	}

	return errors.Join(errs...)
}
