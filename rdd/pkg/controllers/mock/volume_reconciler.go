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
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	mobyvolume "github.com/moby/moby/api/types/volume"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

//go:embed testdata/volumes.json
var testVolumes []byte

// volumeReconcileRequest represents a request to reconcile a volume resource.
type volumeReconcileRequest struct {
	Kind string
	types.NamespacedName
}

type volumeReconciler struct {
	client.Client
	Recorder events.EventRecorder
	// Volumes, by name; filled in during SetupWithManager.
	inspects map[string]mobyvolume.Volume
	// Flag to indicate we have already done the initial creation of volumes.
	created atomic.Bool
}

func getVolumeName(volume mobyvolume.Volume) (namespace, name string) {
	return containerNamespace, volume.Name
}

// Reconcile implements [reconcile.TypedReconciler].
func (r *volumeReconciler) Reconcile(ctx context.Context, req volumeReconcileRequest) (ctrl.Result, error) {
	switch req.Kind {
	case "Namespace":
		return r.reconcileNamespace(ctx, req)
	case containersv1alpha1.VolumeKind:
		return r.reconcileVolume(ctx, req)
	}
	return ctrl.Result{}, fmt.Errorf("unsupported kind: %s", req.Kind)
}

func (r *volumeReconciler) reconcileNamespace(ctx context.Context, req volumeReconcileRequest) (ctrl.Result, error) {
	var errs []error
	log := log.FromContext(ctx)

	var rddNamespace corev1.Namespace
	if err := r.Client.Get(ctx, req.NamespacedName, &rddNamespace); err != nil {
		log.Error(err, "Failed to get namespace", "namespace", req.NamespacedName)
		if apierrors.IsNotFound(err) {
			// Next time the namespace exists, re-create the volumes.  This should
			// never happen, so the log still applies.
			r.created.Store(false)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&rddNamespace) {
		// If the namespace is being deleted, mark volumes as not created; do not
		// reconcile the volumes themselves.  This should never happen, because the
		// namespace vaildator should prevent it from being deleted.
		r.created.Store(false)
		return ctrl.Result{}, nil
	}

	gvk, err := r.Client.GroupVersionKindFor(&rddNamespace)
	if err != nil {
		log.Error(err, "Failed to get GVK for namespace", "namespace", &rddNamespace)
		return ctrl.Result{}, err
	}

	if r.created.Load() {
		// We only create the initial set of volumes once.
		return ctrl.Result{}, nil
	}

	ownerReference := metav1apply.OwnerReference().
		WithAPIVersion(gvk.GroupVersion().String()).
		WithKind(gvk.Kind).
		WithName(rddNamespace.GetName()).
		WithUID(rddNamespace.GetUID()).
		WithBlockOwnerDeletion(true).
		WithController(true)

	for objectName, inspect := range r.inspects {
		err = r.Client.Apply(
			ctx,
			containersv1alpha1apply.
				Volume(objectName, appv1alpha1.AppNamespace).
				WithOwnerReferences(ownerReference),
			client.ForceOwnership,
			client.FieldOwner(controllerLongName),
		)
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
		} else {
			log.V(1).Info("Volume has no readable creation time",
				"volume", inspect.Name, "created", inspect.CreatedAt)
		}
		err = r.Client.Status().Apply(
			ctx,
			containersv1alpha1apply.
				Volume(objectName, appv1alpha1.AppNamespace).
				WithStatus(statusApplyConfig),
			client.ForceOwnership,
			client.FieldOwner(controllerLongName),
		)
		if err != nil {
			errs = append(errs, err)
		}
	}

	err = errors.Join(errs...)
	if err == nil {
		r.created.Store(true)
	}
	return ctrl.Result{}, err
}

func (r *volumeReconciler) reconcileVolume(ctx context.Context, req volumeReconcileRequest) (ctrl.Result, error) {
	log := log.FromContext(ctx)
	var volume containersv1alpha1.Volume
	if err := r.Client.Get(ctx, req.NamespacedName, &volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if base.IsBeingDeleted(&volume) {
		// Ignore volumes that are being deleted
		return ctrl.Result{}, nil
	}
	inspect, ok := r.inspects[req.Name]
	if !ok {
		// Volume not found in the static test data, it should not exist.
		return ctrl.Result{}, r.Delete(ctx, &volume)
	}
	var errs []error

	var rddNamespace corev1.Namespace
	if err := r.Client.Get(ctx, types.NamespacedName{Name: mockNamespaceName}, &rddNamespace); err != nil {
		log.Error(err, "Failed to get namespace", "namespace", mockNamespaceName)
		return ctrl.Result{}, err
	}
	gvk, err := r.Client.GroupVersionKindFor(&rddNamespace)
	if err != nil {
		log.Error(err, "Failed to get GVK for namespace", "namespace", &rddNamespace)
		return ctrl.Result{}, err
	}

	err = r.Client.Apply(
		ctx,
		containersv1alpha1apply.
			Volume(req.Name, req.Namespace).
			WithOwnerReferences(metav1apply.OwnerReference().
				WithAPIVersion(gvk.GroupVersion().String()).
				WithKind(gvk.Kind).
				WithName(rddNamespace.GetName()).
				WithUID(rddNamespace.GetUID()).
				WithBlockOwnerDeletion(true).
				WithController(true)),
		client.ForceOwnership,
		client.FieldOwner(controllerLongName),
	)
	if err != nil {
		errs = append(errs, err)
	}

	statusApplyConfig := containersv1alpha1apply.VolumeStatus().
		WithName(inspect.Name).
		WithNamespace(containerNamespace).
		WithDriver(inspect.Driver).
		WithLabels(inspect.Labels).
		WithOptions(inspect.Options).
		WithMountPoint(inspect.Mountpoint).
		WithScope(inspect.Scope)
	if t, err := time.Parse(time.RFC3339Nano, inspect.CreatedAt); err == nil {
		statusApplyConfig = statusApplyConfig.WithCreatedAt(metav1.NewTime(t))
	} else {
		log.V(1).Info("Volume has no readable creation time",
			"volume", inspect.Name, "created", inspect.CreatedAt)
	}
	err = r.Client.Status().Apply(
		ctx,
		containersv1alpha1apply.
			Volume(req.Name, req.Namespace).
			WithStatus(statusApplyConfig),
		client.ForceOwnership,
		client.FieldOwner(controllerLongName),
	)
	if err != nil {
		errs = append(errs, err)
	}

	return ctrl.Result{}, errors.Join(errs...)
}

func (r *volumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var inspects []mobyvolume.Volume
	if err := json.Unmarshal(testVolumes, &inspects); err != nil {
		return fmt.Errorf("failed to load static test data: %w", err)
	}
	r.inspects = make(map[string]mobyvolume.Volume)
	for _, volume := range inspects {
		r.inspects[sanitizeKubernetesObjectName(volume.Name)] = volume
	}

	return builder.TypedControllerManagedBy[volumeReconcileRequest](mgr).
		Named("mock-volume-reconciler").
		WithLogConstructor(func(req *volumeReconcileRequest) logr.Logger {
			log := mgr.GetLogger().WithValues("controller", "mock-volume-reconciler")
			if req != nil {
				log = log.WithValues(
					"controllerKind", req.Kind,
					req.Kind, klog.KRef(req.Namespace, req.Name),
					"namespace", req.Namespace,
					"name", req.Name,
				)
			}
			return log
		}).
		Watches(&containersv1alpha1.Volume{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []volumeReconcileRequest {
			return []volumeReconcileRequest{
				{
					Kind:           containersv1alpha1.VolumeKind,
					NamespacedName: client.ObjectKeyFromObject(obj),
				},
			}
		})).
		Watches(&corev1.Namespace{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []volumeReconcileRequest {
			return []volumeReconcileRequest{
				{
					Kind:           "Namespace",
					NamespacedName: client.ObjectKeyFromObject(obj),
				},
			}
		}), builder.WithPredicates(predicate.NewPredicateFuncs(func(object client.Object) bool {
			if ns, ok := object.(*corev1.Namespace); ok {
				return ns.GetName() == mockNamespaceName
			}
			return false
		}))).
		Complete(r)
}
