// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// fakeDiscovery satisfies ControllerDiscovery for unit tests.
type fakeDiscovery struct {
	enabled []string
	err     error
}

func (f fakeDiscovery) GetEnabledControllers(_ context.Context) ([]string, error) {
	return f.enabled, f.err
}

func Test_computeSettledCondition(t *testing.T) {
	t.Parallel()

	// makeApp builds an App at the given generation, running spec, and
	// conditions. Callers pass generation=2 so stale ObservedGeneration
	// values have room below.
	makeApp := func(generation int64, running bool, conditions ...metav1.Condition) *v1alpha1.App {
		return &v1alpha1.App{
			ObjectMeta: metav1.ObjectMeta{Generation: generation},
			Spec:       v1alpha1.AppSpec{Running: running},
			Status: v1alpha1.AppStatus{
				Conditions: conditions,
			},
		}
	}

	cond := func(t, reason, message string, status metav1.ConditionStatus, gen int64) metav1.Condition {
		return metav1.Condition{
			Type:               t,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: gen,
		}
	}

	running := func(reason, message string, status metav1.ConditionStatus, gen int64) metav1.Condition {
		return cond(v1alpha1.AppConditionRunning, reason, message, status, gen)
	}
	engine := func(reason, message string, status metav1.ConditionStatus, gen int64) metav1.Condition {
		return cond(v1alpha1.AppConditionContainerEngineReady, reason, message, status, gen)
	}

	tests := []struct {
		name          string
		app           *v1alpha1.App
		engineEnabled bool
		wantStatus    metav1.ConditionStatus
		wantReason    string
		wantMessage   string
	}{
		{
			name:          "no Running condition yet",
			app:           makeApp(2, true),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonWaitingForLimaVM,
			wantMessage:   settledMessageWaitingForLimaVM,
		},
		{
			name:          "in-progress Starting holds Settled false",
			app:           makeApp(2, true, running("Starting", "", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "Starting",
			wantMessage:   settledMessageLimaVMNotReached + "Started",
		},
		// The start/stop failure cases below use synthetic reason names
		// ending in "Failed" to show that computeSettledCondition
		// forwards LimaVM's message rather than reading it. The "Failed"
		// suffix is load-bearing: runningLimaVMMessage only passes the
		// message through when the reason matches HasSuffix("Failed").
		{
			name:          "StartFailed surfaces LimaVM message",
			app:           makeApp(2, true, running("ExplosionFailed", "the virtual machine did not explode", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "ExplosionFailed",
			wantMessage:   "the virtual machine did not explode",
		},
		{
			name:          "StartFailed with empty message falls back to generic text",
			app:           makeApp(2, true, running("ExplosionFailed", "", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "ExplosionFailed",
			wantMessage:   settledMessageLimaVMNotReached + "Started",
		},
		{
			name: "engine disabled short-circuits when VM is Started",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
			),
			engineEnabled: false,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    v1alpha1.AppSettledReasonSettled,
			wantMessage:   settledMessageSettled,
		},
		{
			name: "engine enabled and ready at current generation settles",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    v1alpha1.AppSettledReasonSettled,
			wantMessage:   settledMessageSettled,
		},
		{
			name: "engine enabled but condition missing holds Settled false",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonWaitingForEngine,
			wantMessage:   settledMessageWaitingForEngine,
		},
		{
			name: "engine ready at older generation is stale",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Ready", "engine is ready", metav1.ConditionTrue, 1),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonEngineStale,
			wantMessage:   settledMessageEngineStale,
		},
		{
			name: "engine not ready surfaces its reason and message",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("DilithiumOffline", "warp core is offline", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "DilithiumOffline",
			wantMessage:   "warp core is offline",
		},
		{
			name: "desired stopped + Stopped settles regardless of engine",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    v1alpha1.AppSettledReasonSettled,
			wantMessage:   settledMessageSettled,
		},
		{
			name: "desired stopped but Stopping holds Settled false",
			app: makeApp(2, false,
				running("Stopping", "", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "Stopping",
			wantMessage:   settledMessageLimaVMNotReached + "Stopped",
		},
		{
			name: "StopFailed surfaces LimaVM message",
			app: makeApp(2, false,
				running("ImplosionFailed", "the virtual machine did not implode", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "ImplosionFailed",
			wantMessage:   "the virtual machine did not implode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := computeSettledCondition(tt.app, tt.engineEnabled)
			assert.Equal(t, got.Type, v1alpha1.AppConditionSettled)
			assert.Equal(t, got.Status, tt.wantStatus)
			assert.Equal(t, got.Reason, tt.wantReason)
			assert.Equal(t, got.Message, tt.wantMessage)
			assert.Equal(t, got.ObservedGeneration, tt.app.Generation)
		})
	}
}

func Test_engineEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		discovery ControllerDiscovery
		want      bool
	}{
		{
			name:      "nil discovery returns false",
			discovery: nil,
			want:      false,
		},
		{
			name:      "engine present in discovery returns true",
			discovery: fakeDiscovery{enabled: []string{"app", "lima", "engine"}},
			want:      true,
		},
		{
			name:      "engine absent from discovery returns false",
			discovery: fakeDiscovery{enabled: []string{"app", "lima"}},
			want:      false,
		},
		{
			name:      "discovery error defaults to true so the wait does not return prematurely",
			discovery: fakeDiscovery{err: errors.New("kube-apiserver unreachable")},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &AppReconciler{Discovery: tt.discovery}
			assert.Equal(t, r.engineEnabled(t.Context()), tt.want)
		})
	}
}
