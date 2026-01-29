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
	"time"

	mobyvolume "github.com/moby/moby/api/types/volume"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

//go:embed testdata/volumes.json
var testVolumes []byte

type volumeReconciler struct {
	client.Client
	Recorder events.EventRecorder
	inspects []mobyvolume.Volume
}

// Reconcile implements [reconcile.TypedReconciler].
func (r *volumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var errs []error
	log := log.FromContext(ctx)

	// Check for the CRD to be registered.
	const crdName = "volumes.containers.rancherdesktop.io"
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
		namespacedName := types.NamespacedName{
			Namespace: metav1.NamespaceDefault,
			Name:      inspect.Name,
		}
		targetVolume := containersv1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespacedName.Namespace,
				Name:      namespacedName.Name,
				Labels: map[string]string{
					"namespace": "moby",
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(&rddNamespace, gvk),
				},
			},
			Status: containersv1alpha1.VolumeStatus{
				Driver:     inspect.Driver,
				Labels:     inspect.Labels,
				Options:    inspect.Options,
				MountPoint: inspect.Mountpoint,
				Scope:      inspect.Scope,
			},
		}
		if t, err := time.Parse(time.RFC3339Nano, inspect.CreatedAt); err == nil {
			targetVolume.Status.CreatedAt = metav1.NewTime(t)
		} else if inspect.CreatedAt != "" {
			log.Error(err, "Failed to parse volume created time", "volume", namespacedName, "created", inspect.CreatedAt)
		}
		var existingVolume containersv1alpha1.Volume
		if err := r.Get(ctx, namespacedName, &existingVolume); apierrors.IsNotFound(err) {
			// No existing volume found; create it.  Note that the status subresource
			// is ignored on initial create, so we need to copy it into another object
			// and update it separately.
			targetVolume.DeepCopyInto(&existingVolume)
			if err := r.Create(ctx, &existingVolume); err != nil {
				errs = append(errs, fmt.Errorf("failed to create static volume %s: %w", namespacedName, err))
			} else {
				existingVolume.Status = targetVolume.Status
				if err := r.Status().Update(ctx, &existingVolume); err != nil {
					if apierrors.IsConflict(err) {
						log.V(5).Info("Conflict updating volume status, will retry on next reconcile", "name", namespacedName)
						reconcileResult.RequeueAfter = time.Second
					} else {
						errs = append(errs, fmt.Errorf("failed to update status for static volume %s: %w", namespacedName, err))
					}
				}
			}
		} else if err != nil {
			log.Error(err, "Failed to get static volume", "name", namespacedName)
			errs = append(errs, err)
		} else {
			targetVolume.ResourceVersion = existingVolume.ResourceVersion
			if err := r.Update(ctx, &targetVolume); err != nil {
				if apierrors.IsConflict(err) {
					log.V(5).Info("Conflict updating volume, will retry on next reconcile", "name", namespacedName)
					reconcileResult.RequeueAfter = time.Second
				} else {
					errs = append(errs, fmt.Errorf("failed to update static volume %s: %w", namespacedName, err))
				}
			} else {
				if err := r.Status().Update(ctx, &targetVolume); err != nil {
					if apierrors.IsConflict(err) {
						log.V(5).Info("Conflict updating volume status, will retry on next reconcile", "name", namespacedName)
						reconcileResult.RequeueAfter = time.Second
					} else {
						errs = append(errs, fmt.Errorf("failed to update status for static volume %s: %w", namespacedName, err))
					}
				}
			}
		}
	}

	if len(errs) > 0 {
		return ctrl.Result{}, errors.Join(errs...)
	}

	return reconcileResult, nil
}

func (r *volumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var inspects []mobyvolume.Volume
	if err := json.Unmarshal(testVolumes, &inspects); err != nil {
		return fmt.Errorf("failed to load static test data: %w", err)
	}
	r.inspects = inspects

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-volume-reconciler").
		Watches(
			&containersv1alpha1.Volume{},
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
			if _, ok := object.(*containersv1alpha1.Volume); ok {
				return true
			}
			return false
		})).
		Complete(r)
}
