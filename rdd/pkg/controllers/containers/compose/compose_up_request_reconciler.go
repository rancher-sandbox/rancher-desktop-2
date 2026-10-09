// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// upRequest is the typed reconcile request for upRequestReconciler; it
// carries the UID in addition to the namespaced name, so that process state
// tracked by UID can be correctly cleaned up when the object is deleted (as
// opposed to a standard [ctrl.Request], which only has the namespaced name).
type upRequest struct {
	types.NamespacedName
	types.UID
}

// upRequestReconciler implements the ComposeUpRequest reconcile loop.
type upRequestReconciler struct {
	// ctx is the context that lasts for the lifetime of the reconciler; used
	// for the `docker compose up` process itself, which must outlive any
	// individual reconcile.
	ctx context.Context
	client.Client
	procs        *processTracker
	completionCh chan event.TypedGenericEvent[*v1alpha1.ComposeUpRequest]
}

// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeuprequests/finalizers,verbs=update
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeprojects,verbs=get;list;watch;create;update;patch

// Reconcile implements [reconcile.TypedReconciler].
func (r *upRequestReconciler) Reconcile(ctx context.Context, req upRequest) (ctrl.Result, error) {
	var upReq v1alpha1.ComposeUpRequest
	if err := r.Get(ctx, req.NamespacedName, &upReq); err != nil {
		if apierrors.IsNotFound(err) {
			// The request no longer exists; abort any process still tracked for it.
			if err := r.procs.abort(ctx, req.UID); err != nil {
				logf.FromContext(ctx).V(1).Info(
					"failed to kill process for deleted ComposeUpRequest", "name", req.NamespacedName, "err", err)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	settled := apimeta.FindStatusCondition(upReq.Status.Conditions, v1alpha1.ComposeUpRequestConditionSettled)
	state, hasState := r.procs.get(upReq.GetUID())

	switch {
	case settled != nil && settled.Status == metav1.ConditionTrue:
		if settled.ObservedGeneration < upReq.Generation {
			// The settled condition is for an older generation; treat it as not settled.
			return ctrl.Result{}, r.initiateUp(ctx, &upReq)
		}
		// Already settled (succeeded or failed); nothing further to do here.
		reapDelay := getReapDelay(&upReq)
		targetTime := settled.LastTransitionTime.Add(reapDelay)
		if targetTime.Before(time.Now()) {
			// It's been long enough since it was settled
			//nolint:gocritic // uncheckedInlineErr doesn't understand IgnoreNotFound
			if err := r.Delete(ctx, &upReq); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete ComposeUpRequest %s: %w", req.NamespacedName, err)
			}
			return ctrl.Result{}, nil
		}
		// Trigger a requeue for reaping.
		return ctrl.Result{RequeueAfter: time.Until(targetTime)}, nil
	case hasState && !state.finished:
		// The process is still running; wait for the reconcile triggered on completion.
		return ctrl.Result{}, nil
	case hasState:
		// The process has finished; record the outcome.
		return ctrl.Result{}, r.completeUp(ctx, &upReq, state)
	default:
		// Not yet started; ensure the Compose object exists/is up to date, then
		// kick off `docker compose up`.
		return ctrl.Result{}, r.initiateUp(ctx, &upReq)
	}
}

// initiateUp runs `docker compose up` for the given ComposeUpRequest.  As a
// side effect, the Compose reconciler will see the labels on the created
// containers and create or update a matching Compose object.
func (r *upRequestReconciler) initiateUp(ctx context.Context, upRequest *v1alpha1.ComposeUpRequest) error {
	err := r.procs.run(
		r.ctx,
		upRequest.GetUID(),
		upRequest.GetResourceVersion(),
		upRequest.Spec.WorkingDir,
		upRequest.Spec.Name,
		upRequest.Spec.Configs,
		[]string{"up", "--detach"},
		func() {
			completionEvent := event.TypedGenericEvent[*v1alpha1.ComposeUpRequest]{
				Object: upRequest,
			}
			select {
			case r.completionCh <- completionEvent:
			case <-r.ctx.Done():
			}
		},
	)
	if err != nil {
		return err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.ComposeUpRequest
		if err := r.Get(ctx, client.ObjectKeyFromObject(upRequest), &latest); err != nil {
			return err
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ComposeUpRequestConditionSettled,
			Status:             metav1.ConditionFalse,
			Reason:             v1alpha1.ComposeUpRequestSettledReasonRunning,
			Message:            "docker compose up is running",
			ObservedGeneration: upRequest.Generation,
		})
		changed = apimeta.RemoveStatusCondition(
			&latest.Status.Conditions,
			v1alpha1.ComposeUpRequestConditionFailed,
		) || changed
		if changed {
			return r.Status().Update(ctx, &latest)
		}
		return nil
	})
	if err != nil {
		// Failed to record that the process started; abort it so we retry the
		// whole thing cleanly on the next reconcile.
		_ = r.procs.abort(ctx, upRequest.GetUID())
		return fmt.Errorf(
			"failed to apply status for compose up request %s: %w", client.ObjectKeyFromObject(upRequest), err)
	}
	return nil
}

// completeUp records the outcome of a finished `docker compose up` command, and
// removes the state from the process tracker.
func (r *upRequestReconciler) completeUp(ctx context.Context, upRequest *v1alpha1.ComposeUpRequest, state processState) error {
	failed := state.err != nil
	settledReason := v1alpha1.ComposeUpRequestSettledReasonSucceeded
	message := "docker compose up succeeded"
	if failed {
		settledReason = v1alpha1.ComposeUpRequestSettledReasonFailed
		message = fmt.Sprintf("docker compose up failed: %v: %s", state.err, state.cmd.output())
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest v1alpha1.ComposeUpRequest
		if err := r.Get(ctx, client.ObjectKeyFromObject(upRequest), &latest); err != nil {
			return err
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ComposeUpRequestConditionSettled,
			Status:             metav1.ConditionTrue,
			Reason:             settledReason,
			Message:            message,
			ObservedGeneration: upRequest.Generation,
		})
		if failed {
			changed = apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               v1alpha1.ComposeUpRequestConditionFailed,
				Status:             metav1.ConditionTrue,
				Reason:             v1alpha1.ComposeUpRequestFailedReasonFailed,
				Message:            message,
				ObservedGeneration: upRequest.Generation,
			}) || changed
		} else {
			changed = apimeta.RemoveStatusCondition(
				&latest.Status.Conditions,
				v1alpha1.ComposeUpRequestConditionFailed,
			) || changed
		}
		if changed {
			return r.Status().Update(ctx, &latest)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf(
			"failed to apply status for compose up request %s: %w", client.ObjectKeyFromObject(upRequest), err)
	}
	r.procs.delete(upRequest.GetUID())
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *upRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueRequest := handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []upRequest {
		return []upRequest{{
			NamespacedName: client.ObjectKeyFromObject(obj),
			UID:            obj.GetUID(),
		}}
	})

	return builder.TypedControllerManagedBy[upRequest](mgr).
		Named("compose-up-request-reconciler").
		WatchesRawSource(source.TypedChannel(r.completionCh,
			handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, req *v1alpha1.ComposeUpRequest) []upRequest {
				return []upRequest{{
					NamespacedName: client.ObjectKeyFromObject(req),
					UID:            req.GetUID(),
				}}
			}))).
		Watches(&v1alpha1.ComposeUpRequest{}, enqueueRequest).
		Complete(r)
}
