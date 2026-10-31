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
	"sync"
	"time"

	mobyvolume "github.com/moby/moby/api/types/volume"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

//go:embed testdata/volumes.json
var testVolumes []byte

type volumeReconciler struct {
	client.Client
	Recorder events.EventRecorder
	inspects []mobyvolume.Volume

	// deletedMu guards deleted.
	deletedMu sync.Mutex
	// deleted records the Kubernetes object names (see sanitizeKubernetesObjectName)
	// of volumes that a client has explicitly deleted, e.g. via the GUI's
	// "Delete" action. Reconcile skips re-applying any static testdata entry
	// whose name is in this set, so a deletion sticks instead of being
	// recreated on the next reconcile triggered by the deletion itself.
	deleted map[string]bool
}

func getVolumeName(volume mobyvolume.Volume) (namespace, name string) {
	return containerNamespace, volume.Name
}

// Reconcile implements [reconcile.Reconciler].
func (r *volumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var errs []error
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

	for _, inspect := range r.inspects {
		objectName := sanitizeKubernetesObjectName(inspect.Name)
		if r.isDeleted(objectName) {
			err = r.Client.Delete(ctx, &containersv1alpha1.Volume{
				ObjectMeta: metav1.ObjectMeta{
					Name:      objectName,
					Namespace: appv1alpha1.AppNamespace,
				},
			})
			if client.IgnoreNotFound(err) != nil {
				errs = append(errs, err)
			}
			continue
		}

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
		// Leave createdAt unset when the timestamp will not parse. A
		// substituted date reaches the UI as a real one.
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

	return ctrl.Result{}, errors.Join(errs...)
}

// isDeleted reports whether a client has explicitly deleted the Volume with
// the given Kubernetes object name.
func (r *volumeReconciler) isDeleted(objectName string) bool {
	r.deletedMu.Lock()
	defer r.deletedMu.Unlock()

	return r.deleted[objectName]
}

func (r *volumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var inspects []mobyvolume.Volume
	if err := json.Unmarshal(testVolumes, &inspects); err != nil {
		return fmt.Errorf("failed to load static test data: %w", err)
	}
	r.inspects = inspects
	r.deleted = map[string]bool{}

	ownerHandler := handler.EnqueueRequestForOwner(
		mgr.GetScheme(),
		mgr.GetRESTMapper(),
		&corev1.Namespace{},
		handler.OnlyControllerOwner(),
	)
	// Delegate Update/Generic to the normal owner-enqueueing handler, but on
	// Create clear any prior deletion record (so re-creating a Volume with
	// the same name lets it be reconciled again), and on Delete record the
	// Volume's name so Reconcile knows not to recreate it from the static
	// testdata. Without this, a client (e.g. the GUI's "Delete" action)
	// deleting a Volume would immediately trigger a reconcile of the owning
	// Namespace, which would just re-apply every testdata entry — including
	// the one that was just deleted.
	deleteTrackingHandler := handler.Funcs{
		CreateFunc: func(_ context.Context, evt event.TypedCreateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			r.deletedMu.Lock()
			delete(r.deleted, evt.Object.GetName())
			r.deletedMu.Unlock()
			// To make tests easier, ignore the ownership constraint and enqueue
			// the target namespace directly.
			q.Add(reconcile.Request{NamespacedName: types.NamespacedName{Name: mockNamespaceName}})
		},
		UpdateFunc: ownerHandler.Update,
		DeleteFunc: func(ctx context.Context, evt event.TypedDeleteEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			r.deletedMu.Lock()
			r.deleted[evt.Object.GetName()] = true
			r.deletedMu.Unlock()
			ownerHandler.Delete(ctx, evt, q)
		},
		GenericFunc: ownerHandler.Generic,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		Named("mock-volume-reconciler").
		Watches(&containersv1alpha1.Volume{}, deleteTrackingHandler).
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
