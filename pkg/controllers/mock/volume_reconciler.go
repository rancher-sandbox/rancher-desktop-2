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
)

//go:embed testdata/volumes.json
var testVolumes []byte

type volumeReconciler struct {
	client.Client
	Recorder events.EventRecorder
	inspects []mobyvolume.Volume
}

func getVolumeName(volume mobyvolume.Volume) (namespace, name string) {
	return containerNamespace, volume.Name
}

// Reconcile implements [reconcile.TypedReconciler].
func (r *volumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var errs []error
	log := log.FromContext(ctx)

	// Check for the CRD to be registered.
	const crdName = "volumes.containers.rancherdesktop.io"
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
		err = r.Client.Apply(
			ctx,
			containersv1alpha1apply.
				Volume(sanitizeKubernetesObjectName(inspect.Name), apiNamespace).
				WithOwnerReferences(ownerReference),
			client.ForceOwnership,
			client.FieldOwner(controllerLongName))
		if err != nil {
			errs = append(errs, err)
		}

		namespace, name := getVolumeName(inspect)
		statusApplyConfig := containersv1alpha1apply.VolumeStatus().
			WithName(name).
			WithNamespace(namespace).
			WithDriver(inspect.Driver).
			WithLabels(inspect.Labels).
			WithOptions(inspect.Options).
			WithMountPoint(inspect.Mountpoint).
			WithScope(inspect.Scope)
		if t, err := time.Parse(time.RFC3339Nano, inspect.CreatedAt); err == nil {
			statusApplyConfig = statusApplyConfig.WithCreatedAt(metav1.NewTime(t))
		} else if inspect.CreatedAt != "" {
			log.Error(err, "Failed to parse volume created time", "volume", inspect.Name, "created", inspect.CreatedAt)
		}
		err = r.Client.Status().Apply(
			ctx,
			containersv1alpha1apply.
				Volume(sanitizeKubernetesObjectName(inspect.Name), apiNamespace).
				WithStatus(statusApplyConfig),
			client.ForceOwnership,
			client.FieldOwner(controllerLongName))
		if err != nil {
			errs = append(errs, err)
		}
	}

	return ctrl.Result{}, errors.Join(errs...)
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
