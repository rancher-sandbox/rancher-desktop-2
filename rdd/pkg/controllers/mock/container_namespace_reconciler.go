// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mobyvolume "github.com/moby/moby/api/types/volume"

	corev1 "k8s.io/api/core/v1"
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

type containerNamespaceReconciler struct {
	client.Client
	Recorder events.EventRecorder
	volumes  []mobyvolume.Volume
}

func (r *containerNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

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

	namespaces := map[string]struct{}{containerNamespace: {}}
	for _, volume := range r.volumes {
		namespace, _ := getVolumeName(volume)
		namespaces[namespace] = struct{}{}
	}

	var errs []error
	for namespace := range namespaces {
		applyConfig := containersv1alpha1apply.ContainerNamespace(namespace, apiNamespace).
			WithOwnerReferences(metav1apply.OwnerReference().
				WithAPIVersion(gvk.GroupVersion().String()).
				WithKind(gvk.Kind).
				WithName(rddNamespace.GetName()).
				WithUID(rddNamespace.GetUID()).
				WithBlockOwnerDeletion(true).
				WithController(true))
		err = r.Client.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner(controllerLongName))
		if err != nil {
			errs = append(errs, err)
		}
	}

	return ctrl.Result{}, errors.Join(errs...)
}

func (r *containerNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var volumes []mobyvolume.Volume
	if err := json.Unmarshal(testVolumes, &volumes); err != nil {
		return fmt.Errorf("failed to load static test volume data: %w", err)
	}
	r.volumes = volumes

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-container-namespace-reconciler").
		Watches(
			&containersv1alpha1.ContainerNamespace{},
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
			if _, ok := object.(*containersv1alpha1.ContainerNamespace); ok {
				return true
			}
			return false
		})).
		Complete(r)
}
