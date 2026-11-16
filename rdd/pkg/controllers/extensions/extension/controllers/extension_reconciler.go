// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/atomicmap"
)

// ExtensionReconciler reconciles an Extension object across all of its lifecycle
// conditions: Installed, Extracted, Started, and Ready.
type ExtensionReconciler struct {
	client.Client
	ctx context.Context
	events.EventRecorder
	requeueCh chan event.TypedGenericEvent[*v1alpha1.Extension]

	// Extracted condition state:
	extractor *extensionExtractor

	// Installed condition state:
	scripts  atomicmap.AtomicMap[types.UID, scriptState]
	engine   engine
	engineMu sync.Mutex
}

// extensionReconcileRequest identifies the resource kind and name to reconcile.
type extensionReconcileRequest struct {
	kind string
	types.NamespacedName
}

// requeue is a dummy error type to trigger a requeue.
type requeueError time.Duration

func (requeueError) Error() string {
	return "dummy"
}

// NewExtensionReconciler creates a new unified ExtensionReconciler.
func NewExtensionReconciler(ctx context.Context, mgr ctrl.Manager) *ExtensionReconciler {
	c := mgr.GetClient()
	extractor := &extensionExtractor{
		Client:        c,
		EventRecorder: mgr.GetEventRecorder("extension-extractor"),
		ctx:           ctx,
	}
	extractor.prepare = extractor.prepareImpl
	extractor.extractMetadata = extractor.extractMetadataImpl
	extractor.extractIcon = extractor.extractIconImpl
	extractor.extractUI = extractor.extractUIImpl
	extractor.extractExecutable = extractor.extractExecutableImpl
	extractor.extractStep = extractor.extractStepImpl
	extractor.extractContainer = extractor.extractContainerImpl

	r := &ExtensionReconciler{
		Client:        c,
		ctx:           ctx,
		EventRecorder: mgr.GetEventRecorder("extension-controller"),
		requeueCh:     make(chan event.TypedGenericEvent[*v1alpha1.Extension], 1024),
		extractor:     extractor,
	}
	return r
}

var _ reconcile.TypedReconciler[extensionReconcileRequest] = &ExtensionReconciler{}

// Reconcile implements the reconcile.TypedReconciler interface for the unified ExtensionReconciler.
func (r *ExtensionReconciler) Reconcile(ctx context.Context, req extensionReconcileRequest) (ctrl.Result, error) {
	switch req.kind {
	case appv1alpha1.AppKind:
		var app appv1alpha1.App
		err := r.Get(ctx, req.NamespacedName, &app)
		if apierrors.IsNotFound(err) {
			return r.reconcileApp(ctx, nil)
		} else if err != nil {
			return ctrl.Result{}, err
		}
		return r.reconcileApp(ctx, &app)

	case v1alpha1.ExtensionKind:
		ext := &v1alpha1.Extension{}
		err := r.Get(ctx, req.NamespacedName, ext)
		if err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		if base.IsBeingDeleted(ext) {
			return r.reconcileDelete(ctx, ext)
		}

		err = r.reconcile(ctx, ext)
		if requeue, ok := errors.AsType[requeueError](err); ok {
			return ctrl.Result{RequeueAfter: time.Duration(requeue)}, nil
		}
		return ctrl.Result{}, err

	default:
		return ctrl.Result{}, fmt.Errorf("unexpected reconcile request kind: %s", req.kind)
	}
}

func (r *ExtensionReconciler) reconcile(ctx context.Context, ext *v1alpha1.Extension) error {
	// Step 1: Reconcile Installed condition.
	err := r.reconcileInstalled(ctx, ext)
	if err != nil {
		return err
	}

	// Step 2: Reconcile Extracted condition if currently extracting.
	installed := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionInstalled)
	if installed != nil {
		if installed.Reason == v1alpha1.ExtensionInstalledReasonExtracting {
			if err := r.reconcileExtracted(ctx, ext); err != nil {
				return err
			}
		}
	}

	return nil
}

// reconcileDelete coordinates teardown across all conditions when an Extension is deleted.
func (r *ExtensionReconciler) reconcileDelete(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	return ctrl.Result{}, fmt.Errorf("not implemented")
}

// SetupWithManager sets up the unified controller with the Manager using typed requests.
func (r *ExtensionReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if r.requeueCh == nil {
		r.requeueCh = make(chan event.TypedGenericEvent[*v1alpha1.Extension], 1024)
	}

	// We need to be able to look up images by the tag, not just by ID.
	if err := base.IndexCRDFields(ctx, &containersv1alpha1.Image{}, mgr); err != nil {
		return err
	}

	enqueueOwner := handler.TypedEnqueueRequestsFromMapFunc(
		func(_ context.Context, obj client.Object) []extensionReconcileRequest {
			var requests []extensionReconcileRequest
			for _, owner := range obj.GetOwnerReferences() {
				if owner.Kind == v1alpha1.ExtensionKind {
					requests = append(requests, extensionReconcileRequest{
						kind:      v1alpha1.ExtensionKind,
						Namespace: obj.GetNamespace(),
						Name:      owner.Name,
					})
				}
			}
			return requests
		},
	)

	return builder.TypedControllerManagedBy[extensionReconcileRequest](mgr).
		Watches(&v1alpha1.Extension{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []extensionReconcileRequest {
			return []extensionReconcileRequest{{
				kind:           v1alpha1.ExtensionKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
			}}
		})).
		Watches(&appv1alpha1.App{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []extensionReconcileRequest {
			return []extensionReconcileRequest{{
				kind:           appv1alpha1.AppKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
			}}
		})).
		Watches(&containersv1alpha1.ImagePullRequest{}, enqueueOwner).
		Watches(&containersv1alpha1.ComposeProject{}, enqueueOwner).
		Watches(&containersv1alpha1.ComposeUpRequest{}, enqueueOwner).
		WatchesRawSource(source.TypedChannel(
			r.requeueCh,
			handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, ext *v1alpha1.Extension) []extensionReconcileRequest {
				if ext == nil {
					return nil
				}
				return []extensionReconcileRequest{{
					kind:           v1alpha1.ExtensionKind,
					NamespacedName: client.ObjectKeyFromObject(ext),
				}}
			}),
		)).
		Named("extension-reconciler").
		Complete(r)
}

func (r *ExtensionReconciler) setCondition(ctx context.Context, ext *v1alpha1.Extension, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	r.Eventf(ext, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, "%s: %s", reason, message)
	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               conditionType,
			Status:             status,
			ObservedGeneration: latest.Generation,
			Reason:             reason,
			Message:            message,
		})
		if !changed {
			return nil
		}
		if err := r.Status().Update(ctx, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		// On a successful update, return immediately to avoid triggering more
		// reconciliations.
		return requeueError(0)
	})
}
