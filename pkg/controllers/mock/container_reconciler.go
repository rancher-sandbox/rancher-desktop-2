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

	mobycontainer "github.com/moby/moby/api/types/container"

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

	reconcileResult := ctrl.Result{}

	for _, inspect := range r.inspects {
		namespacedName := types.NamespacedName{
			Namespace: metav1.NamespaceDefault,
			Name:      inspect.ID,
		}
		namespace, name, _ := strings.Cut(inspect.Name, "/")
		if namespace == "" {
			namespace = "moby"
		}
		state := containersv1alpha1.ContainerStatusCreated
		if inspect.State.Running {
			state = containersv1alpha1.ContainerStatusRunning
		}
		targetContainer := containersv1alpha1.Container{
			ObjectMeta: metav1.ObjectMeta{
				Name:      inspect.ID,
				Namespace: metav1.NamespaceDefault,
				Labels: map[string]string{
					"namespace": namespace,
					"name":      name,
				},
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(&rddNamespace, gvk),
				},
			},
			Spec: containersv1alpha1.ContainerSpec{
				State: state,
			},
			Status: containersv1alpha1.ContainerStatus{
				Path:   inspect.Path,
				Args:   inspect.Args,
				Image:  inspect.Image,
				Labels: inspect.Config.Labels,
				Status: containersv1alpha1.ContainerStatusValue(inspect.State.Status),
			},
		}
		for portName, ports := range inspect.NetworkSettings.Ports {
			containerPort := containersv1alpha1.ContainerPort{
				Name: portName.String(),
			}
			for _, port := range ports {
				containerPort.Bindings = append(containerPort.Bindings, containersv1alpha1.ContainerPortBinding{
					HostIP:   port.HostIP.String(),
					HostPort: port.HostPort,
				})
			}
			targetContainer.Status.Ports = append(targetContainer.Status.Ports, containerPort)
		}
		var existingContainer containersv1alpha1.Container
		canUpdateStatus := true
		if err := r.Get(ctx, namespacedName, &existingContainer); apierrors.IsNotFound(err) {
			if err := r.Create(ctx, &targetContainer); err != nil {
				errs = append(errs, fmt.Errorf("failed to create static container %s: %w", namespacedName, err))
				canUpdateStatus = false
			}
		} else if err != nil {
			log.Error(err, "Failed to get static container", "name", namespacedName)
			errs = append(errs, err)
		} else {
			targetContainer.ResourceVersion = existingContainer.ResourceVersion
			targetContainer.Status.Conditions = existingContainer.Status.Conditions
			if err := r.Update(ctx, &targetContainer); err != nil {
				if apierrors.IsConflict(err) {
					log.V(5).Info("Conflict updating container, will retry on next reconcile", "name", namespacedName)
					reconcileResult.RequeueAfter = time.Second
				} else {
					errs = append(errs, fmt.Errorf("failed to update static container %s: %w", namespacedName, err))
				}
				canUpdateStatus = false
			}
		}
		if canUpdateStatus {
			if err := r.Status().Update(ctx, &targetContainer); err != nil {
				if apierrors.IsConflict(err) {
					log.V(5).Info("Conflict updating container status, will retry on next reconcile", "name", namespacedName)
					reconcileResult.RequeueAfter = time.Second
				} else {
					errs = append(errs, fmt.Errorf("failed to update status for static container %s: %w", namespacedName, err))
				}
			}
		}
	}

	if len(errs) > 0 {
		return ctrl.Result{}, errors.Join(errs...)
	}

	return reconcileResult, nil
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
