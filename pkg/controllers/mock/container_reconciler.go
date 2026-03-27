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

	mobycontainer "github.com/moby/moby/api/types/container"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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

//go:embed testdata/containers.json
var testContainers []byte

type containerReconciler struct {
	client.Client
	Recorder events.EventRecorder
	inspects []mobycontainer.InspectResponse
}

func (r *containerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var errs []error
	log := log.FromContext(ctx)

	// Check for the CRD to be registered.
	const crdName = "containers.containers.rancherdesktop.io"
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
		namespace, name, _ := strings.Cut(inspect.Name, "/")
		if namespace == "" {
			namespace = containerNamespace
		}
		state := containersv1alpha1.ContainerStatusCreated
		if inspect.State.Running {
			state = containersv1alpha1.ContainerStatusRunning
		}
		applyConfig := containersv1alpha1apply.Container(inspect.ID, apiNamespace).
			WithOwnerReferences(ownerReference).
			WithSpec(containersv1alpha1apply.ContainerSpec().WithState(state))

		err := r.Client.Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner(controllerLongName))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to apply container %s/%s: %w", namespace, name, err))
		}

		applyStatus := containersv1alpha1apply.ContainerStatus().
			WithName(name).
			WithNamespace(namespace).
			WithPath(inspect.Path).
			WithArgs(inspect.Args...).
			WithImage(inspect.Image).
			WithLabels(inspect.Config.Labels).
			WithStatus(containersv1alpha1.ContainerStatusValue(inspect.State.Status))
		var applyPorts []*containersv1alpha1apply.ContainerPortApplyConfiguration
		for portName, ports := range inspect.NetworkSettings.Ports {
			var bindings []*containersv1alpha1apply.ContainerPortBindingApplyConfiguration
			for _, port := range ports {
				bindings = append(bindings, containersv1alpha1apply.ContainerPortBinding().
					WithHostIP(port.HostIP.String()).
					WithHostPort(port.HostPort))
			}
			applyPorts = append(applyPorts, containersv1alpha1apply.ContainerPort().
				WithName(portName.String()).
				WithBindings(bindings...))
		}
		applyStatus.WithPorts(applyPorts...)
		applyConfig = containersv1alpha1apply.Container(inspect.ID, apiNamespace).
			WithStatus(applyStatus)

		err = r.Client.SubResource("status").Apply(ctx, applyConfig, client.ForceOwnership, client.FieldOwner(controllerLongName))
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to apply container status %s/%s: %w", namespace, name, err))
		}
	}

	return ctrl.Result{}, errors.Join(errs...)
}

func (r *containerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var inspects []mobycontainer.InspectResponse
	if err := json.Unmarshal(testContainers, &inspects); err != nil {
		return fmt.Errorf("failed to load static test data: %w", err)
	}
	r.inspects = inspects

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-container-reconciler").
		Watches(
			&containersv1alpha1.Container{},
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
			if _, ok := object.(*containersv1alpha1.Container); ok {
				return true
			}
			return false
		})).
		Complete(r)
}
