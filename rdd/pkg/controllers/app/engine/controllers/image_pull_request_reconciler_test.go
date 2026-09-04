// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"gotest.tools/v3/assert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

type testEngine struct {
	pullImageErr  error
	pullImageHook func(ctx context.Context, repoTag string, onProgress func(int64, int64, int64, string), onComplete func(error))
	pullCalled    bool
	pullRepoTag   string
}

func (*testEngine) alive() bool { return true }

func (*testEngine) stop() {}

func (*testEngine) processContainerAction(_ context.Context, _ *containersv1alpha1.Container) error {
	return nil
}

func (*testEngine) hasTTY(_ context.Context, _ *containersv1alpha1.Container) (bool, error) {
	return false, nil
}

func (*testEngine) getLogs(_ context.Context, _ *containersv1alpha1.Container, _ ...engineLogOptions) (io.ReadCloser, error) {
	return nil, nil
}

func (e *testEngine) pullImage(
	ctx context.Context,
	repoTag string,
	onProgress func(int64, int64, int64, string),
	onComplete func(error),
) error {
	e.pullCalled = true
	e.pullRepoTag = repoTag
	if e.pullImageHook != nil {
		e.pullImageHook(ctx, repoTag, onProgress, onComplete)
	}
	return e.pullImageErr
}

func (*testEngine) deleteContainer(_ context.Context, _ *containersv1alpha1.Container) error {
	return nil
}

func (*testEngine) deleteImage(_ context.Context, _ *containersv1alpha1.Image) error { return nil }

func (*testEngine) deleteVolume(_ context.Context, _ *containersv1alpha1.Volume) error { return nil }

func newEngineImagePullReconcilerTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, containersv1alpha1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...)
	for _, obj := range objs {
		if req, ok := obj.(*containersv1alpha1.ImagePullRequest); ok {
			builder = builder.WithStatusSubresource(req)
		}
	}

	return builder.Build()
}

// TestEngineImagePullRequestReconcileEngineNotReady verifies that reconcile is
// a no-op when no engine is currently connected.
func TestEngineImagePullRequestReconcileEngineNotReady(t *testing.T) {
	r := &EngineReconciler{
		Client: newEngineImagePullReconcilerTestClient(t),
	}
	result, err := r.reconcileImagePullRequest(t.Context(), engineRequest{
		Kind: containersv1alpha1.ImagePullRequestKind,
		NamespacedName: types.NamespacedName{
			Namespace: "rancher-desktop",
			Name:      "pull-request",
		},
		UID: types.UID("pull-request-uid"),
	})
	assert.NilError(t, err)
	assert.Equal(t, result, ctrl.Result{})
}

// TestEngineImagePullRequestReconcileUIDMismatchCancelsExistingState verifies
// that when the object at a name has a different UID, reconcile treats it as a
// delete/recreate event, cancels old pull state, and returns without error.
func TestEngineImagePullRequestReconcileUIDMismatchCancelsExistingState(t *testing.T) {
	reqUID := types.UID("pull-request-uid")
	liveReq := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("different-uid"),
		},
	}
	cancelContext, cancel := context.WithCancelCause(context.Background())
	r := &EngineReconciler{
		Client: newEngineImagePullReconcilerTestClient(t, liveReq),
		engine: &testEngine{},
		imagePullRequestState: map[types.UID]imagePullRequestState{
			reqUID: {cancel: cancel},
		},
	}

	result, err := r.reconcileImagePullRequest(t.Context(), engineRequest{
		Kind: containersv1alpha1.ImagePullRequestKind,
		NamespacedName: types.NamespacedName{
			Namespace: "rancher-desktop",
			Name:      "pull-request",
		},
		UID: reqUID,
	})
	assert.NilError(t, err)
	assert.Equal(t, result, ctrl.Result{})
	assert.Equal(t, context.Cause(cancelContext), error(imagePullRequestFailedReasonDeleted))
	_, exists := r.imagePullRequestState[reqUID]
	assert.Assert(t, !exists)
}

// TestEngineImagePullRequestReconcileBulkProcessesPendingRequests verifies that
// the req.UID == "" path lists and starts pending pull requests in namespace.
func TestEngineImagePullRequestReconcileBulkProcessesPendingRequests(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			RepoTag: "alpine:latest",
		},
	}
	engine := &testEngine{}
	r := &EngineReconciler{
		Client:                newEngineImagePullReconcilerTestClient(t, req),
		engine:                engine,
		imagePullRequestState: map[types.UID]imagePullRequestState{},
		watcherCtx:            t.Context(),
	}

	result, err := r.reconcileImagePullRequest(t.Context(), engineRequest{
		Kind: containersv1alpha1.ImagePullRequestKind,
		NamespacedName: types.NamespacedName{
			Namespace: "rancher-desktop",
		},
	})
	assert.NilError(t, err)
	assert.Assert(t, result.RequeueAfter > 0, "requeue expected")
	assert.Assert(t, engine.pullCalled)
	assert.Equal(t, engine.pullRepoTag, "alpine:latest")
	state := r.imagePullRequestState[req.UID]
	assert.Assert(t, state.cancel != nil)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, r.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
	settledCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionSettled)
	assert.Assert(t, settledCond != nil && settledCond.Status == metav1.ConditionFalse && settledCond.Reason == "Pulling")
}

// TestEngineImagePullRequestReconcileTerminalReasonSuccess verifies that a
// queued success terminal reason sets Settled=True (ImagePulled) and
// Failed=False (Succeeded).
func TestEngineImagePullRequestReconcileTerminalReasonSuccess(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
	}
	c := newEngineImagePullReconcilerTestClient(t, req)
	r := &EngineReconciler{
		Client:                c,
		imagePullRequestState: map[types.UID]imagePullRequestState{},
	}

	r.setImagePullRequestTerminalReason(req, imagePullRequestFailedReasonPullSucceeded, "image pulled successfully")
	err := r.reconcileImagePullRequestTerminalReason(t.Context(), req)
	assert.NilError(t, err)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(req), updated))

	settledCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionSettled)
	assert.Assert(t, settledCond != nil && settledCond.Status == metav1.ConditionTrue && settledCond.Reason == "ImagePulled")
	failedCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionFailed)
	assert.Assert(t, failedCond != nil && failedCond.Status == metav1.ConditionFalse && failedCond.Reason == "Succeeded")
}

// TestEngineImagePullRequestReconcileTerminalReasonFailure verifies that a
// queued failure terminal reason sets Settled=True (Errored), sets Failed=True
// with the expected reason, and clears in-memory state.
func TestEngineImagePullRequestReconcileTerminalReasonFailure(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			LastUpdateTime: now,
			Start:          1,
			Current:        2,
			Total:          3,
			Units:          "bytes",
		},
	}
	c := newEngineImagePullReconcilerTestClient(t, req)
	r := &EngineReconciler{
		Client:                c,
		imagePullRequestState: map[types.UID]imagePullRequestState{},
	}

	r.setImagePullRequestTerminalReason(req, imagePullRequestFailedReasonTimeout, "pull timed out")
	err := r.reconcileImagePullRequestTerminalReason(t.Context(), req)
	assert.NilError(t, err)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(req), updated))

	settledCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionSettled)
	assert.Assert(t, settledCond != nil && settledCond.Status == metav1.ConditionTrue && settledCond.Reason == "Errored")
	failedCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionFailed)
	assert.Assert(t, failedCond != nil && failedCond.Status == metav1.ConditionTrue && failedCond.Reason == "PullTimeout")
	_, exists := r.imagePullRequestState[req.UID]
	assert.Assert(t, !exists)
}

// TestEngineImagePullRequestReconcileDeletedRequestCancelsPull verifies that
// reconciling a request that no longer exists cancels any in-progress pull with
// the Deleted cause and removes request state.
func TestEngineImagePullRequestReconcileDeletedRequestCancelsPull(t *testing.T) {
	reqUID := types.UID("pull-request-uid")
	cancelContext, cancel := context.WithCancelCause(context.Background())
	r := &EngineReconciler{
		Client: newEngineImagePullReconcilerTestClient(t),
		engine: &testEngine{},
		imagePullRequestState: map[types.UID]imagePullRequestState{
			reqUID: {cancel: cancel},
		},
	}

	result, err := r.reconcileImagePullRequest(t.Context(), engineRequest{
		Kind: containersv1alpha1.ImagePullRequestKind,
		NamespacedName: types.NamespacedName{
			Namespace: "rancher-desktop",
			Name:      "pull-request",
		},
		UID: reqUID,
	})
	assert.NilError(t, err)
	assert.Equal(t, result, ctrl.Result{})
	assert.Equal(t, context.Cause(cancelContext), error(imagePullRequestFailedReasonDeleted))
	_, exists := r.imagePullRequestState[reqUID]
	assert.Assert(t, !exists)
}

// TestEngineImagePullRequestReconcileSinglePullingTimeoutQueuesTerminalReason
// verifies that a long-running pull past pullTimeout queues PullTimeout as the
// terminal reason and requests an immediate requeue.
func TestEngineImagePullRequestReconcileSinglePullingTimeoutQueuesTerminalReason(t *testing.T) {
	reqUID := types.UID("pull-request-uid")
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       reqUID,
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			RepoTag: "alpine:latest",
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			LastUpdateTime: metav1.NewTime(time.Now().UTC().Add(-2 * pullTimeout)),
		},
	}
	apimeta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{
		Type:   containersv1alpha1.ImagePullRequestConditionSettled,
		Status: metav1.ConditionFalse,
		Reason: "Pulling",
	})

	r := &EngineReconciler{
		imagePullRequestState: map[types.UID]imagePullRequestState{},
	}
	result, err := r.reconcileSingleImagePullRequest(t.Context(), &testEngine{}, req)
	assert.NilError(t, err)
	assert.Assert(t, result.RequeueAfter > 0)
	state := r.imagePullRequestState[reqUID]
	assert.Equal(t, state.desiredReason, imagePullRequestFailedReasonTimeout)
	assert.Assert(t, strings.Contains(state.desiredMessage, `image pull request for "alpine:latest" timed out`))
}

// TestEngineImagePullRequestReconcileSinglePullingRequeuesHalfTimeout verifies
// that an in-progress, non-timed-out pull requeues halfway to timeout.
func TestEngineImagePullRequestReconcileSinglePullingRequeuesHalfTimeout(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			RepoTag: "alpine:latest",
		},
		Status: containersv1alpha1.ImagePullRequestStatus{
			LastUpdateTime: metav1.Now(),
		},
	}
	apimeta.SetStatusCondition(&req.Status.Conditions, metav1.Condition{
		Type:   containersv1alpha1.ImagePullRequestConditionSettled,
		Status: metav1.ConditionFalse,
		Reason: "Pulling",
	})

	r := &EngineReconciler{
		imagePullRequestState: map[types.UID]imagePullRequestState{},
	}
	result, err := r.reconcileSingleImagePullRequest(t.Context(), &testEngine{}, req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, pullTimeout/2)
}

// TestEngineImagePullRequestReconcileSingleQueuedTerminalReason verifies that
// when terminal reason is already queued in memory, reconcile applies status
// and requeues for post-terminal TTL handling.
func TestEngineImagePullRequestReconcileSingleQueuedTerminalReason(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
	}
	r := &EngineReconciler{
		Client: newEngineImagePullReconcilerTestClient(t, req),
		imagePullRequestState: map[types.UID]imagePullRequestState{
			req.UID: {
				desiredReason:  imagePullRequestFailedReasonPullFailed,
				desiredMessage: "failed to pull image",
			},
		},
	}

	result, err := r.reconcileSingleImagePullRequest(t.Context(), &testEngine{}, req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, terminalRequestTTL/2)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, r.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
	settledCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionSettled)
	assert.Assert(t, settledCond != nil && settledCond.Status == metav1.ConditionTrue && settledCond.Reason == "Errored")
	failedCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionFailed)
	assert.Assert(t, failedCond != nil && failedCond.Status == metav1.ConditionTrue && failedCond.Reason == "PullFailed")
}

// TestEngineImagePullRequestReconcileSingleImmediatePullError verifies that an
// immediate engine pull failure queues PullFailed and requeues shortly.
func TestEngineImagePullRequestReconcileSingleImmediatePullError(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			RepoTag: "alpine:latest",
		},
	}
	engine := &testEngine{pullImageErr: errors.New("boom")}
	r := &EngineReconciler{
		Client:                newEngineImagePullReconcilerTestClient(t, req),
		imagePullRequestState: map[types.UID]imagePullRequestState{},
		watcherCtx:            t.Context(),
	}

	result, err := r.reconcileSingleImagePullRequest(t.Context(), engine, req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Second)
	state := r.imagePullRequestState[req.UID]
	assert.Equal(t, state.desiredReason, imagePullRequestFailedReasonPullFailed)
	assert.Assert(t, strings.Contains(state.desiredMessage, `failed to pull image "alpine:latest": boom`))
}

// TestEngineImagePullRequestReconcileSingleProgressCallbackUpdatesStatus
// verifies that engine progress callbacks update status fields and keep
// Settled=False/Pulling.
func TestEngineImagePullRequestReconcileSingleProgressCallbackUpdatesStatus(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			RepoTag: "alpine:latest",
		},
	}
	engine := &testEngine{
		pullImageHook: func(_ context.Context, _ string, onProgress func(int64, int64, int64, string), _ func(error)) {
			onProgress(1, 2, 3, "bytes")
		},
	}
	r := &EngineReconciler{
		Client:                newEngineImagePullReconcilerTestClient(t, req),
		imagePullRequestState: map[types.UID]imagePullRequestState{},
		watcherCtx:            t.Context(),
	}

	result, err := r.reconcileSingleImagePullRequest(t.Context(), engine, req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, pullTimeout/2)

	// Progress only updates on a timer.
	<-time.After(200 * time.Millisecond)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, r.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
	assert.Equal(t, updated.Status.Start, int64(1))
	assert.Equal(t, updated.Status.Current, int64(2))
	assert.Equal(t, updated.Status.Total, int64(3))
	assert.Equal(t, updated.Status.Units, "bytes")
	settledCond := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionSettled)
	assert.Assert(t, settledCond != nil && settledCond.Status == metav1.ConditionFalse && settledCond.Reason == "Pulling")
}

// TestEngineImagePullRequestReconcileSingleCompleteReasonMapping verifies that
// completion errors are mapped to the expected terminal reason.
func TestEngineImagePullRequestReconcileSingleCompleteReasonMapping(t *testing.T) {
	tests := []struct {
		name       string
		pullErr    error
		wantReason imagePullRequestFailedReason
	}{
		{
			name:       "success",
			pullErr:    nil,
			wantReason: imagePullRequestFailedReasonPullSucceeded,
		},
		{
			name:       "invalid argument",
			pullErr:    fmt.Errorf("bad request: %w", cerrdefs.ErrInvalidArgument),
			wantReason: imagePullRequestFailedReasonInvalidArgument,
		},
		{
			name:       "unauthorized",
			pullErr:    fmt.Errorf("auth denied: %w", cerrdefs.ErrUnauthenticated),
			wantReason: imagePullRequestFailedReasonUnauthorized,
		},
		{
			name:       "generic failure",
			pullErr:    errors.New("pull failed"),
			wantReason: imagePullRequestFailedReasonPullFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := &containersv1alpha1.ImagePullRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pull-request",
					Namespace: "rancher-desktop",
					UID:       types.UID("pull-request-uid"),
				},
				Spec: containersv1alpha1.ImagePullRequestSpec{
					RepoTag: "alpine:latest",
				},
			}
			engine := &testEngine{
				pullImageHook: func(_ context.Context, _ string, _ func(int64, int64, int64, string), onComplete func(error)) {
					onComplete(tc.pullErr)
				},
			}
			r := &EngineReconciler{
				Client:                newEngineImagePullReconcilerTestClient(t, req),
				imagePullRequestState: map[types.UID]imagePullRequestState{},
				watcherCtx:            t.Context(),
			}

			result, err := r.reconcileSingleImagePullRequest(t.Context(), engine, req)
			assert.NilError(t, err)
			assert.Equal(t, result.RequeueAfter, pullTimeout/2)

			// onComplete applies the terminal reason asynchronously in a goroutine;
			// pause a bit to let it remove the state.
			<-time.After(100 * time.Millisecond)
			r.imagePullRequestMu.Lock()
			_, hasState := r.imagePullRequestState[req.UID]
			r.imagePullRequestMu.Unlock()
			assert.Equal(t, hasState, false)

			var updated containersv1alpha1.ImagePullRequest
			assert.NilError(t, r.Get(t.Context(), client.ObjectKeyFromObject(req), &updated))
			failedCondition := apimeta.FindStatusCondition(updated.Status.Conditions, containersv1alpha1.ImagePullRequestConditionFailed)
			assert.Assert(t, failedCondition != nil)
			assert.Equal(t, failedCondition.Reason, string(tc.wantReason))
		})
	}
}

// TestSetImagePullRequestTerminalReasonCancelsExistingPull verifies that
// setting a terminal reason cancels a currently running pull and records the
// desired terminal state.
func TestSetImagePullRequestTerminalReasonCancelsExistingPull(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
	}
	cancelContext, cancel := context.WithCancelCause(context.Background())
	r := &EngineReconciler{
		imagePullRequestState: map[types.UID]imagePullRequestState{
			req.UID: {cancel: cancel},
		},
	}

	returnedCancel := r.setImagePullRequestTerminalReason(req, imagePullRequestFailedReasonPullFailed, "pull failed")
	returnedCancel()
	assert.Equal(t, context.Cause(cancelContext), error(imagePullRequestFailedReasonPullFailed))
	state := r.imagePullRequestState[req.UID]
	assert.Equal(t, state.desiredReason, imagePullRequestFailedReasonPullFailed)
	assert.Equal(t, state.desiredMessage, "pull failed")
	assert.Assert(t, state.cancel == nil)
}

// TestEngineImagePullRequestReconcileTerminalReasonNoDesiredNoop verifies that
// terminal reason reconciliation is a no-op when no desired reason exists.
func TestEngineImagePullRequestReconcileTerminalReasonNoDesiredNoop(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			UID:       types.UID("pull-request-uid"),
		},
	}
	r := &EngineReconciler{
		Client:                newEngineImagePullReconcilerTestClient(t, req),
		imagePullRequestState: map[types.UID]imagePullRequestState{},
	}

	assert.NilError(t, r.reconcileImagePullRequestTerminalReason(t.Context(), req))
	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, r.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
	assert.Equal(t, len(updated.Status.Conditions), 0)
}

// TestEngineImagePullRequestReconcileTerminalDeletesOwnerlessAfterTimer
// verifies that a settled ownerless request is deleted after terminalRequestTTL
// has elapsed.
func TestEngineImagePullRequestReconcileTerminalDeletesOwnerlessAfterTimer(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
		},
	}
	c := newEngineImagePullReconcilerTestClient(t, req)
	r := &EngineReconciler{Client: c}

	lastTransitionTime := metav1.NewTime(time.Now().UTC().Add(-2 * terminalRequestTTL))
	result, err := r.reconcileTerminalImagePullRequest(t.Context(), req, lastTransitionTime)
	assert.NilError(t, err)
	assert.Equal(t, result, (ctrl.Result{}))

	got := &containersv1alpha1.ImagePullRequest{}
	getErr := c.Get(t.Context(), client.ObjectKeyFromObject(req), got)
	assert.Assert(t, apierrors.IsNotFound(getErr))
}

// TestEngineImagePullRequestReconcileTerminalOwnedNoDelete verifies that a
// settled request with owner references is retained even after
// terminalRequestTTL has elapsed.
func TestEngineImagePullRequestReconcileTerminalOwnedNoDelete(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			OwnerReferences: []metav1.OwnerReference{
				{UID: types.UID("owner-uid")},
			},
		},
	}
	c := newEngineImagePullReconcilerTestClient(t, req)
	r := &EngineReconciler{Client: c}

	lastTransitionTime := metav1.NewTime(time.Now().UTC().Add(-2 * terminalRequestTTL))
	result, err := r.reconcileTerminalImagePullRequest(t.Context(), req, lastTransitionTime)
	assert.NilError(t, err)
	assert.Equal(t, result, (ctrl.Result{}))

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
}

// TestEngineImagePullRequestReconcileTerminalRequeuesBeforeTimerEvenIfOwned
// verifies that before terminalRequestTTL elapses, terminal requests requeue
// for later processing regardless of ownership.
func TestEngineImagePullRequestReconcileTerminalRequeuesBeforeTimerEvenIfOwned(t *testing.T) {
	req := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-request",
			Namespace: "rancher-desktop",
			OwnerReferences: []metav1.OwnerReference{
				{UID: types.UID("owner-uid")},
			},
		},
	}
	c := newEngineImagePullReconcilerTestClient(t, req)
	r := &EngineReconciler{Client: c}

	lastTransitionTime := metav1.NewTime(time.Now().UTC().Add(terminalRequestTTL / 2))
	result, err := r.reconcileTerminalImagePullRequest(t.Context(), req, lastTransitionTime)
	assert.NilError(t, err)
	assert.Assert(t, result.RequeueAfter > 0)

	updated := &containersv1alpha1.ImagePullRequest{}
	assert.NilError(t, c.Get(t.Context(), client.ObjectKeyFromObject(req), updated))
}
