// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	mobyimage "github.com/moby/moby/api/types/image"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
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
		if apierrors.IsNotFound(err) {
			log.Info("CRD not found, requeuing", "crd", crdName)
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		log.Error(err, "Failed to get CRD", "crd", crdName)
		return ctrl.Result{}, err
	}

	var rddNamespace corev1.Namespace
	if err := r.Client.Get(ctx, req.NamespacedName, &rddNamespace); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Namespace not found, requeuing", "namespace", req.NamespacedName)
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		log.Error(err, "Failed to get namespace", "namespace", req.NamespacedName)
		return ctrl.Result{}, err
	}
	gvk, err := r.Client.GroupVersionKindFor(&rddNamespace)
	if err != nil {
		log.Error(err, "Failed to get GVK for namespace", "namespace", &rddNamespace)
		return ctrl.Result{}, err
	}

	var reconcileResult ctrl.Result
	for _, inspect := range r.inspects {
		imageName := inspect.ID
		if len(inspect.RepoTags) > 0 {
			imageName = inspect.RepoTags[0]
		}
		templateImage := containersv1alpha1.Image{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: metav1.NamespaceDefault,
				Labels:    map[string]string{},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(&rddNamespace, gvk),
				},
			},
			Status: containersv1alpha1.ImageStatus{
				ID:           inspect.ID,
				RepoDigests:  inspect.RepoDigests,
				Architecture: inspect.Architecture,
				OS:           inspect.Os,
				Size:         inspect.Size,
				Labels:       inspect.Config.Labels,
			},
		}
		if t, err := time.Parse(time.RFC3339Nano, inspect.Created); err == nil {
			templateImage.Status.CreatedAt = metav1.NewTime(t)
		} else if inspect.Created != "" {
			log.Error(err, "Failed to parse image created time", "image", imageName, "created", inspect.Created)
		}
		if len(inspect.RepoTags) > 0 {
			templateImage.ObjectMeta.GenerateName = sanitizeKubernetesObjectName(inspect.ID) + "-"
			for _, tag := range inspect.RepoTags {
				targetImage := templateImage.DeepCopy()
				targetImage.Labels["namespace"] = "moby"
				targetImage.Status.RepoTag = tag
				if err := r.upsertImage(ctx, targetImage); err != nil {
					if apierrors.IsConflict(err) {
						log.V(5).Info("Conflict updating image, will retry on next reconcile", "image", tag)
						reconcileResult.RequeueAfter = time.Second
					} else {
						errs = append(errs, err)
					}
				}
			}
		} else {
			// No tags; create a single dangling image.
			templateImage.ObjectMeta.Name = sanitizeKubernetesObjectName(inspect.ID)
			if err := r.upsertImage(ctx, &templateImage); err != nil {
				if apierrors.IsConflict(err) {
					log.V(5).Info("Conflict updating image, will retry on next reconcile", "image", imageName)
					reconcileResult.RequeueAfter = time.Second
				} else {
					errs = append(errs, err)
				}
			}
		}
	}

	if len(errs) > 0 {
		log.V(9).Info("Reconciled with errors", "count", len(r.inspects), "errors", len(errs))
		return ctrl.Result{}, errors.Join(errs...)
	}

	return reconcileResult, nil
}

// upsertImage creates or updates the given Image resource.  The passed in image
// will be updated with the results.
func (r *imageReconciler) upsertImage(ctx context.Context, image *containersv1alpha1.Image) error {
	imageName := image.Status.RepoTag
	if imageName == "" {
		imageName = image.Status.ID
	}

	var existingImages containersv1alpha1.ImageList
	originalImage := image.DeepCopy()
	err := r.List(ctx, &existingImages,
		client.MatchingFieldsSelector{Selector: fields.AndSelectors(
			fields.OneTermEqualSelector(".status.id", image.Status.ID),
			fields.OneTermEqualSelector(".status.repoTag", image.Status.RepoTag),
		)})
	if apierrors.IsNotFound(err) || (err == nil && len(existingImages.Items) == 0) {
		if err := r.Create(ctx, image); err != nil {
			return fmt.Errorf("failed to create static image %s: %w", imageName, err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to find existing static images for %s: %w", imageName, err)
	} else if len(existingImages.Items) == 1 {
		existingImages.Items[0].DeepCopyInto(image)
	}
	// We either found an existing image, or we created a new one; in the former
	// case, we need to update the status.  In the latter case, the status field
	// isn't updated in the initial create, so we need to set it.
	originalImage.Status.DeepCopyInto(&image.Status)
	if err := r.Status().Update(ctx, image); err != nil {
		return fmt.Errorf("failed to update static image %s status: %w", imageName, err)
	}
	return nil
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
