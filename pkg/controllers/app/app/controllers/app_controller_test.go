// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

func Test_computeSettledCondition(t *testing.T) {
	t.Parallel()

	// makeApp builds an App carrying the given conditions. Generation
	// defaults to 2 so stale ObservedGeneration values have room below.
	makeApp := func(generation int64, running bool, conds ...metav1.Condition) *v1alpha1.App {
		app := &v1alpha1.App{Spec: v1alpha1.AppSpec{Running: running}}
		app.Generation = generation
		app.Status.Conditions = append(app.Status.Conditions, conds...)
		return app
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
			wantReason:    "WaitingForLimaVM",
			wantMessage:   "Waiting for LimaVM to report its state",
		},
		{
			name:          "in-progress Starting holds Settled false",
			app:           makeApp(2, true, running("Starting", "", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "Starting",
			wantMessage:   "LimaVM has not yet reached Started",
		},
		{
			name:          "StartFailed surfaces LimaVM message",
			app:           makeApp(2, true, running("StartFailed", "template failed to parse", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "StartFailed",
			wantMessage:   "template failed to parse",
		},
		{
			name:          "StartFailed with empty message falls back to generic text",
			app:           makeApp(2, true, running("StartFailed", "", metav1.ConditionFalse, 2)),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "StartFailed",
			wantMessage:   "LimaVM has not yet reached Started",
		},
		{
			name: "engine disabled short-circuits when VM is Started",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
			),
			engineEnabled: false,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "Settled",
			wantMessage:   "App has reached the desired state",
		},
		{
			name: "engine enabled and ready at current generation settles",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "Settled",
			wantMessage:   "App has reached the desired state",
		},
		{
			name: "engine enabled but condition missing holds Settled false",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "WaitingForEngine",
			wantMessage:   "Waiting for container engine condition",
		},
		{
			name: "engine ready at older generation is stale",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Ready", "engine is ready", metav1.ConditionTrue, 1),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "EngineStale",
			wantMessage:   "Container engine has not yet observed this generation",
		},
		{
			name: "engine not ready surfaces its reason and message",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Connecting", "waiting for Docker socket", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "Connecting",
			wantMessage:   "waiting for Docker socket",
		},
		{
			name: "desired stopped + Stopped settles regardless of engine",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    "Settled",
			wantMessage:   "App has reached the desired state",
		},
		{
			name: "desired stopped but Stopping holds Settled false",
			app: makeApp(2, false,
				running("Stopping", "", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "Stopping",
			wantMessage:   "LimaVM has not yet reached Stopped",
		},
		{
			name: "StopFailed surfaces LimaVM message",
			app: makeApp(2, false,
				running("StopFailed", "qemu process refused SIGTERM", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    "StopFailed",
			wantMessage:   "qemu process refused SIGTERM",
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
