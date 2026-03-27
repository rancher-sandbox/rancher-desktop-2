// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type kubeNamespaceReconciler struct {
	client.Client
	Recorder events.EventRecorder
}

// Reconcile implements [reconcile.TypedReconciler].
func (r *kubeNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	ownerReference := metav1apply.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(rddNamespace.GetName()).
		WithUID(rddNamespace.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true)

	err = r.Client.Apply(
		ctx,
		corev1apply.Namespace(apiNamespace).
			WithOwnerReferences(ownerReference),
		client.ForceOwnership,
		client.FieldOwner(controllerLongName),
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *kubeNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-kube-namespace-reconciler").
		WithEventFilter(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if _, ok := object.(*corev1.Namespace); ok {
				return object.GetName() == mockNamespaceName
			}
			return false
		})).
		Complete(r)
}
