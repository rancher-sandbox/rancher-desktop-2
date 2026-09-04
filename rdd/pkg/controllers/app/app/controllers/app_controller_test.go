// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"text/template"

	"gotest.tools/v3/assert"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
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
			Generation: generation,
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
	pathMgmt := func(reason, message string, status metav1.ConditionStatus, gen int64) metav1.Condition {
		return cond(v1alpha1.AppConditionPathManagementReady, reason, message, status, gen)
	}

	tests := []struct {
		name                  string
		app                   *v1alpha1.App
		addPath               v1alpha1.AddPathStrategy
		engineEnabled         bool
		kubernetesEnabled     bool
		templateOutOfDate     bool
		pathManagementEnabled bool
		wantStatus            metav1.ConditionStatus
		wantReason            string
		wantMessage           string
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
			name: "desired stopped + Stopped settles when engine condition is current",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
				engine(v1alpha1.EngineReasonStopped, "Container engine stopped", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    v1alpha1.AppSettledReasonSettled,
			wantMessage:   settledMessageSettled,
		},
		{
			name: "desired stopped + NotApplicable settles (containerd backend)",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
				engine(v1alpha1.EngineReasonNotApplicable, "no mirroring for containerd", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionTrue,
			wantReason:    v1alpha1.AppSettledReasonSettled,
			wantMessage:   settledMessageSettled,
		},
		{
			// The engine reconciler stamps Connected/M+1 on the first reconcile
			// after spec.running is set to false (while the VM is still stopping).
			// That must not satisfy the wait: Settled must require a terminal
			// engine reason (Stopped or NotApplicable), not just any condition at
			// the current generation.
			name: "desired stopped but engine still Connected at current generation waits",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
				engine(v1alpha1.EngineReasonConnected, "engine synced", metav1.ConditionTrue, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonEngineStale,
			wantMessage:   settledMessageEngineStale,
		},
		{
			name: "desired stopped + Stopped waits when engine condition is absent",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonEngineStale,
			wantMessage:   settledMessageEngineStale,
		},
		{
			name: "desired stopped + Stopped waits when engine condition is stale",
			app: makeApp(2, false,
				running("Stopped", "VM is stopped", metav1.ConditionFalse, 2),
				engine(v1alpha1.EngineReasonConnected, "", metav1.ConditionTrue, 1),
			),
			engineEnabled: true,
			wantStatus:    metav1.ConditionFalse,
			wantReason:    v1alpha1.AppSettledReasonEngineStale,
			wantMessage:   settledMessageEngineStale,
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
		// Kubernetes-gating cases.
		{
			name: "kubernetes disabled does not gate Settled",
			app: makeApp(2, true,
				running("Started", "VM is running", metav1.ConditionTrue, 2),
				engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
			),
			engineEnabled:     true,
			kubernetesEnabled: true,
			// spec.kubernetes.enabled == false (default) → kube gate skipped
			wantStatus:  metav1.ConditionTrue,
			wantReason:  v1alpha1.AppSettledReasonSettled,
			wantMessage: settledMessageSettled,
		},
		{
			name: "kubernetes enabled but condition missing holds Settled false",
			app: func() *v1alpha1.App {
				a := makeApp(2, true,
					running("Started", "VM is running", metav1.ConditionTrue, 2),
					engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
				)
				a.Spec.Kubernetes.Enabled = true
				return a
			}(),
			engineEnabled:     true,
			kubernetesEnabled: true,
			wantStatus:        metav1.ConditionFalse,
			wantReason:        v1alpha1.AppSettledReasonWaitingForKubernetes,
			wantMessage:       settledMessageWaitingForKubernetes,
		},
		{
			name: "kubernetes ready at stale generation blocks Settled",
			app: func() *v1alpha1.App {
				a := makeApp(2, true,
					running("Started", "VM is running", metav1.ConditionTrue, 2),
					engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
					cond(v1alpha1.AppConditionKubernetesReady, v1alpha1.AppKubernetesReasonReady, "ready", metav1.ConditionTrue, 1),
				)
				a.Spec.Kubernetes.Enabled = true
				return a
			}(),
			engineEnabled:     true,
			kubernetesEnabled: true,
			wantStatus:        metav1.ConditionFalse,
			wantReason:        v1alpha1.AppSettledReasonKubernetesStale,
			wantMessage:       settledMessageKubernetesStale,
		},
		{
			name: "kubernetes not ready surfaces its reason",
			app: func() *v1alpha1.App {
				a := makeApp(2, true,
					running("Started", "VM is running", metav1.ConditionTrue, 2),
					engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
					cond(v1alpha1.AppConditionKubernetesReady, v1alpha1.AppKubernetesReasonProbing, "waiting", metav1.ConditionFalse, 2),
				)
				a.Spec.Kubernetes.Enabled = true
				return a
			}(),
			engineEnabled:     true,
			kubernetesEnabled: true,
			wantStatus:        metav1.ConditionFalse,
			wantReason:        v1alpha1.AppKubernetesReasonProbing,
			wantMessage:       "waiting",
		},
		{
			name: "all conditions ready with kubernetes settles",
			app: func() *v1alpha1.App {
				a := makeApp(2, true,
					running("Started", "VM is running", metav1.ConditionTrue, 2),
					engine("Ready", "engine is ready", metav1.ConditionTrue, 2),
					cond(v1alpha1.AppConditionKubernetesReady, v1alpha1.AppKubernetesReasonReady, "ready", metav1.ConditionTrue, 2),
				)
				a.Spec.Kubernetes.Enabled = true
				return a
			}(),
			engineEnabled:     true,
			kubernetesEnabled: true,
			wantStatus:        metav1.ConditionTrue,
			wantReason:        v1alpha1.AppSettledReasonSettled,
			wantMessage:       settledMessageSettled,
		},
		{
			// Even with every feeding condition ready at the current
			// generation, a template the LimaVM has not yet applied holds
			// Settled false — this is the premature-settle guard.
			name: "stale template holds Settled false despite ready conditions",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
			),
			engineEnabled:     true,
			templateOutOfDate: true,
			wantStatus:        metav1.ConditionFalse,
			wantReason:        v1alpha1.AppSettledReasonApplyingTemplate,
			wantMessage:       settledMessageApplyingTemplate,
		},
		{
			// PATH management enabled but no condition yet: hold Settled false.
			name: "path management enabled but not yet reported holds Settled false",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
			),
			addPath:               v1alpha1.AddPathFront,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.AppSettledReasonWaitingForPathManagement,
			wantMessage:           settledMessageWaitingForPathManagement,
		},
		{
			name: "stale path management observed generation holds Settled false",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonApplied, "PATH management applied", metav1.ConditionTrue, 1),
			),
			addPath:               v1alpha1.AddPathFront,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.AppSettledReasonPathManagementStale,
			wantMessage:           settledMessagePathManagementStale,
		},
		{
			name: "failed path management surfaces its reason and message",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonFailed, "cannot write .zshrc", metav1.ConditionFalse, 2),
			),
			addPath:               v1alpha1.AddPathFront,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.PathManagementReasonFailed,
			wantMessage:           "cannot write .zshrc",
		},
		{
			// Under the manual default the user opted out of PATH management, so a
			// permanent failure (a hand-edited dotfile with a dangling marker,
			// reported as Malformed) is one we can't repair and must not block
			// Settled for unrelated settings.
			name: "manual strategy ignores a permanent (malformed) path management failure",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonMalformed, "cannot parse .zshrc", metav1.ConditionFalse, 2),
			),
			addPath:               v1alpha1.AddPathManual,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionTrue,
			wantReason:            v1alpha1.AppSettledReasonSettled,
			wantMessage:           settledMessageSettled,
		},
		{
			// A repairable failure (I/O, read-only home) over an intact block does
			// gate under manual: the reconciler requeues and clears it, so --wait
			// shouldn't report success over a residue that's about to go away.
			name: "manual strategy waits on a repairable path management failure",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonFailed, "cannot write .zshrc", metav1.ConditionFalse, 2),
			),
			addPath:               v1alpha1.AddPathManual,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.PathManagementReasonFailed,
			wantMessage:           "cannot write .zshrc",
		},
		{
			// Switching to manual still runs a removal, so Settled must wait for
			// the reconciler to observe the current generation even though the
			// stale condition is only Applied, not a failure.
			name: "manual strategy still waits for a stale condition",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonApplied, "PATH management applied", metav1.ConditionTrue, 1),
			),
			addPath:               v1alpha1.AddPathManual,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.AppSettledReasonPathManagementStale,
			wantMessage:           settledMessagePathManagementStale,
		},
		{
			// Manual also waits for the first report: the reconciler still runs
			// and writes a condition, so a nil one means it hasn't caught up yet.
			name: "manual strategy waits for the first path management report",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
			),
			addPath:               v1alpha1.AddPathManual,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionFalse,
			wantReason:            v1alpha1.AppSettledReasonWaitingForPathManagement,
			wantMessage:           settledMessageWaitingForPathManagement,
		},
		{
			name: "path management ready at current generation settles",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
				pathMgmt(v1alpha1.PathManagementReasonApplied, "PATH management applied", metav1.ConditionTrue, 2),
			),
			addPath:               v1alpha1.AddPathFront,
			engineEnabled:         true,
			pathManagementEnabled: true,
			wantStatus:            metav1.ConditionTrue,
			wantReason:            v1alpha1.AppSettledReasonSettled,
			wantMessage:           settledMessageSettled,
		},
		{
			// When path management is not enabled its condition is ignored,
			// even if absent, so Settled still reaches True.
			name: "path management disabled ignores its condition",
			app: makeApp(2, true,
				running("Started", "Lima instance is running", metav1.ConditionTrue, 2),
				engine(v1alpha1.EngineReasonConnected, "Container engine synced", metav1.ConditionTrue, 2),
			),
			engineEnabled:         true,
			pathManagementEnabled: false,
			wantStatus:            metav1.ConditionTrue,
			wantReason:            v1alpha1.AppSettledReasonSettled,
			wantMessage:           settledMessageSettled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.app.Spec.Application.AddPath = tt.addPath
			got := computeSettledCondition(tt.app, settledInputs{
				engineEnabled:         tt.engineEnabled,
				kubernetesEnabled:     tt.kubernetesEnabled,
				templateUpToDate:      !tt.templateOutOfDate,
				pathManagementEnabled: tt.pathManagementEnabled,
			})
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

func Test_pathManagementEnabled(t *testing.T) {
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
			name:      "path management present in discovery returns true",
			discovery: fakeDiscovery{enabled: []string{"app", "lima", "path-management"}},
			want:      true,
		},
		{
			name:      "path management absent from discovery returns false",
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
			assert.Equal(t, r.pathManagementEnabled(t.Context()), tt.want)
		})
	}
}

func Test_applySpecToTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		spec            v1alpha1.AppSpec
		status          v1alpha1.AppStatus
		wantPortLine    string
		wantEnabledLine string
	}{
		{
			name: "kubernetes enabled with port set emits the port",
			spec: v1alpha1.AppSpec{
				Kubernetes: v1alpha1.KubernetesSpec{Enabled: true, Version: "1.32.0"},
			},
			status:          v1alpha1.AppStatus{KubernetesPort: 7443},
			wantPortLine:    "  KUBERNETES_PORT: 7443",
			wantEnabledLine: "  KUBERNETES_ENABLED: true",
		},
		{
			name: "kubernetes disabled with port zero emits zero",
			spec: v1alpha1.AppSpec{
				Kubernetes: v1alpha1.KubernetesSpec{Enabled: false},
			},
			status:          v1alpha1.AppStatus{KubernetesPort: 0},
			wantPortLine:    "  KUBERNETES_PORT: 0",
			wantEnabledLine: "  KUBERNETES_ENABLED: false",
		},
		{
			name: "kubernetes disabled but port previously set still emits port",
			spec: v1alpha1.AppSpec{
				Kubernetes: v1alpha1.KubernetesSpec{Enabled: false},
			},
			status:          v1alpha1.AppStatus{KubernetesPort: 7444},
			wantPortLine:    "  KUBERNETES_PORT: 7444",
			wantEnabledLine: "  KUBERNETES_ENABLED: false",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := applySpecToTemplate("", tt.spec, tt.status.KubernetesPort)
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(got, tt.wantPortLine),
				"expected output to contain %q, got:\n%s", tt.wantPortLine, got)
			assert.Assert(t, strings.Contains(got, tt.wantEnabledLine),
				"expected output to contain %q, got:\n%s", tt.wantEnabledLine, got)
		})
	}
}

// Test_applySpecToTemplate_VMResources checks that cpus and memory are appended
// only when set, since they are no longer part of the base template.
func Test_applySpecToTemplate_VMResources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		spec       v1alpha1.AppSpec
		wantCPUs   string // expected "cpus:" line, or "" if none should appear
		wantMemory string // expected "memory:" line, or "" if none should appear
	}{
		{
			name:       "unset cpus and memory append no lines",
			spec:       v1alpha1.AppSpec{},
			wantCPUs:   "",
			wantMemory: "",
		},
		{
			name:     "spec cpus appends a cpus line",
			spec:     v1alpha1.AppSpec{VirtualMachine: v1alpha1.VirtualMachineSpec{CPUs: 4}},
			wantCPUs: "cpus: 4",
		},
		{
			name:       "spec memory appends a memory line in bytes",
			spec:       v1alpha1.AppSpec{VirtualMachine: v1alpha1.VirtualMachineSpec{Memory: mustParseQuantity("4Gi")}},
			wantMemory: `memory: "4294967296"`,
		},
		{
			name: "spec cpus and memory append both lines",
			spec: v1alpha1.AppSpec{VirtualMachine: v1alpha1.VirtualMachineSpec{
				CPUs:   4,
				Memory: mustParseQuantity("6Gi"),
			}},
			wantCPUs:   "cpus: 4",
			wantMemory: `memory: "6442450944"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := applySpecToTemplate("base", tt.spec, 0)
			assert.NilError(t, err)

			if tt.wantCPUs == "" {
				assert.Assert(t, !strings.Contains(got, "cpus:"),
					"expected no cpus line, got:\n%s", got)
			} else {
				assert.Assert(t, strings.Contains(got, tt.wantCPUs),
					"expected output to contain %q, got:\n%s", tt.wantCPUs, got)
			}

			if tt.wantMemory == "" {
				assert.Assert(t, !strings.Contains(got, "memory:"),
					"expected no memory line, got:\n%s", got)
			} else {
				assert.Assert(t, strings.Contains(got, tt.wantMemory),
					"expected output to contain %q, got:\n%s", tt.wantMemory, got)
			}
		})
	}
}

// newTestScheme returns a scheme with all types the AppReconciler touches.
func newTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	assert.NilError(t, corev1.AddToScheme(s))
	assert.NilError(t, v1alpha1.AddToScheme(s))
	assert.NilError(t, limav1alpha1.AddToScheme(s))
	return s
}

func Test_Reconcile_resolvesKubernetesPort(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)

	app := &v1alpha1.App{
		Name: appName,
		Spec: v1alpha1.AppSpec{
			Running:    false,
			Kubernetes: v1alpha1.KubernetesSpec{Enabled: true, Version: "1.32.0"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &AppReconciler{
		Client:           c,
		Scheme:           scheme,
		LimaTemplateData: "",
	}

	req := ctrl.Request{Name: appName}

	// First reconcile: adds the cleanup finalizer and returns early.
	_, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)

	// Second reconcile: past the finalizer, discovers KubernetesPort == 0,
	// calls ResolvePort, and persists a non-zero port to Status.
	_, err = r.Reconcile(context.Background(), req)
	assert.NilError(t, err)

	result := &v1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, result))
	assert.Assert(t, result.Status.KubernetesPort != 0,
		"expected Status.KubernetesPort to be set after reconcile, got 0")
}

func Test_Reconcile_clearsKubernetesPortOnDisable(t *testing.T) {
	t.Parallel()

	scheme := newTestScheme(t)

	// App starts with Kubernetes disabled but a stale port from a previous enable.
	app := &v1alpha1.App{
		Name: appName,
		Spec: v1alpha1.AppSpec{
			Running:    false,
			Kubernetes: v1alpha1.KubernetesSpec{Enabled: false},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	// Inject a stale port directly into status (simulates a prior enable cycle).
	app.Status.KubernetesPort = 7443
	assert.NilError(t, c.Status().Update(context.Background(), app))

	r := &AppReconciler{
		Client:           c,
		Scheme:           scheme,
		LimaTemplateData: "",
	}

	req := ctrl.Request{Name: appName}

	// First reconcile: adds the cleanup finalizer and returns early.
	_, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)

	// Second reconcile: Kubernetes disabled + port set → clears the port.
	_, err = r.Reconcile(context.Background(), req)
	assert.NilError(t, err)

	result := &v1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, result))
	assert.Equal(t, result.Status.KubernetesPort, 0,
		"expected Status.KubernetesPort to be cleared when Kubernetes is disabled")
}

func Test_networkSetupExtraArgs(t *testing.T) {
	t.Run("unset RDD_KEEP_LOGS yields no extra args", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "")
		t.Setenv("RDD_TRACE_PACKETS", "")
		assert.Equal(t, networkSetupExtraArgs(), "")
	})
	t.Run("RDD_TRACE_PACKETS without RDD_KEEP_LOGS yields no extra args", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "")
		t.Setenv("RDD_TRACE_PACKETS", "1")
		assert.Equal(t, networkSetupExtraArgs(), "")
	})
	t.Run("RDD_KEEP_LOGS without tracing yields append only", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "1")
		t.Setenv("RDD_TRACE_PACKETS", "")
		assert.Equal(t, networkSetupExtraArgs(), "--vm-switch-logfile-append")
	})
	t.Run("RDD_TRACE_PACKETS adds the trace flag", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "1")
		t.Setenv("RDD_TRACE_PACKETS", "1")
		assert.Equal(t, networkSetupExtraArgs(), "--vm-switch-logfile-append --trace-packets")
	})
}

func Test_guestLogDir(t *testing.T) {
	t.Run("unset RDD_KEEP_LOGS yields no log dir", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "")
		assert.Equal(t, guestLogDir(), "")
	})
	t.Run("set RDD_KEEP_LOGS yields the instance log dir", func(t *testing.T) {
		t.Setenv("RDD_KEEP_LOGS", "1")
		assert.Equal(t, guestLogDir(), toLinuxPath(instance.LogDir()))
	})
}

// renderProvisionContent mirrors Lima's executeGuestTemplate: it runs the guest
// template engine over a provision file's content with the given Param map, the
// only datum the network-setup drop-in references.
func renderProvisionContent(t *testing.T, content string, param map[string]string) string {
	t.Helper()
	tmpl, err := template.New("").Parse(content)
	assert.NilError(t, err)
	var out bytes.Buffer
	assert.NilError(t, tmpl.Execute(&out, map[string]any{"Param": param}))
	return out.String()
}

func Test_limaTemplate_networkSetupExtraArgsDropin(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("../lima-template.yaml")
	assert.NilError(t, err)

	var doc struct {
		Provision []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"provision"`
	}
	assert.NilError(t, yaml.Unmarshal(data, &doc))

	const dropinPath = "/etc/systemd/system/network-setup.service.d/extra-args.conf"
	var content string
	for _, p := range doc.Provision {
		if p.Path == dropinPath {
			content = p.Content
			break
		}
	}
	assert.Assert(t, content != "", "no provision entry writes %s", dropinPath)

	// Empty param renders the distro's own default (an empty value).
	blank := renderProvisionContent(t, content, map[string]string{"NETWORK_SETUP_EXTRA_ARGS": ""})
	assert.Assert(t, strings.Contains(blank, `Environment="NETWORK_SETUP_EXTRA_ARGS="`),
		"got:\n%s", blank)

	// The value is a single env var. network-setup.service's ExecStart references
	// it unbraced as $NETWORK_SETUP_EXTRA_ARGS, which systemd word-splits into
	// separate arguments; braced ${...} would pass it as one argument.
	got := renderProvisionContent(t, content,
		map[string]string{"NETWORK_SETUP_EXTRA_ARGS": "--vm-switch-logfile-append --trace-packets"})
	assert.Assert(t, strings.Contains(got,
		`Environment="NETWORK_SETUP_EXTRA_ARGS=--vm-switch-logfile-append --trace-packets"`),
		"got:\n%s", got)
}

func mustParseQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
