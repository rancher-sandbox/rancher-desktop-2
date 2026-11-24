// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package service

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/component-base/version"
	"k8s.io/klog/v2"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// Setting -v on the serve command must only update the logging
// configuration, which logsapi.ValidateAndApply later applies to klog.
// Flag sets are merged in map order, so the test builds the command
// repeatedly to make both orders all but certain to occur.
func TestServeCommandVerbosityFlagIsTheLoggingConfiguration(t *testing.T) {
	klogFlags := flag.NewFlagSet("klog", flag.PanicOnError)
	klog.InitFlags(klogFlags)
	t.Cleanup(func() { assert.NilError(t, klogFlags.Set("v", "0")) })

	for range 50 {
		assert.NilError(t, klogFlags.Set("v", "0"))
		command := NewServeCommand(context.Background())
		assert.NilError(t, command.Flags().Set("v", "4"))
		assert.Assert(t, !klog.V(4).Enabled(), "-v is bound to klog, not to the logging configuration")
	}
}

// checkReadiness decides from the discovery ConfigMap alone and never
// requests /readyz, so it reports a ConfigMap left over from a previous run
// as not ready without waiting. The serve command creates the ConfigMap only
// once its API server is ready, so for a missing or leftover ConfigMap
// checkReadiness reports that it is waiting for the API server.
func TestCheckReadinessJudgesDiscoveryConfigMap(t *testing.T) {
	since := time.Now().Truncate(time.Second)
	ready := map[string]string{controllers.ReadyAnnotation: "true"}
	tests := []struct {
		name        string
		created     time.Time
		annotations map[string]string
		status      int
		wantPhase   startupPhase
		wantReady   bool
	}{
		{"leftover from a previous run", since.Add(-time.Hour), ready, http.StatusOK, phaseAPIServer, false},
		{"not created yet", since, nil, http.StatusNotFound, phaseAPIServer, false},
		{"API server unavailable", since, nil, http.StatusServiceUnavailable, phaseAPIServer, false},
		{"fresh without the ready annotation", since, nil, http.StatusOK, phaseControllers, false},
		{"fresh and ready", since, ready, http.StatusOK, phaseControllers, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var readyzHits atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(version.Get())
			})
			path := "GET /api/v1/namespaces/" + controllers.RDDSystemNamespace + "/configmaps/" + controllers.ControllerManagerConfigMapName
			mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) {
				if tt.status != http.StatusOK {
					http.Error(w, http.StatusText(tt.status), tt.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
					ObjectMeta: metav1.ObjectMeta{
						Name:              controllers.ControllerManagerConfigMapName,
						Namespace:         controllers.RDDSystemNamespace,
						CreationTimestamp: metav1.NewTime(tt.created),
						Annotations:       tt.annotations,
					},
				})
			})
			mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
				readyzHits.Add(1)
				http.Error(w, "[-]poststarthook/rbac/bootstrap-roles failed: reason withheld\nreadyz check failed", http.StatusInternalServerError)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var phases []startupPhase
			err := checkReadiness(ctx, func() (*rest.Config, error) { return &rest.Config{Host: server.URL}, nil },
				since, func(p startupPhase) { phases = append(phases, p) })

			assert.NilError(t, ctx.Err(), "checkReadiness waited until the deadline")
			assert.Equal(t, readyzHits.Load(), int32(0), "checkReadiness requested /readyz")
			if tt.wantReady {
				assert.NilError(t, err)
			} else {
				assert.Assert(t, err != nil)
				assert.DeepEqual(t, phases, []startupPhase{tt.wantPhase})
			}
		})
	}
}

// checkReadiness returns when its context ends, even if the control plane
// accepts the request and never answers, as a frozen serve does.
func TestCheckReadinessStopsAtTheDeadlineWhenTheServerHangs(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(server.Close)
	// Cleanups run last-in first-out, so the handler returns before Close
	// waits for it.
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- checkReadiness(ctx, func() (*rest.Config, error) { return &rest.Config{Host: server.URL}, nil },
			time.Now(), func(startupPhase) {})
	}()
	select {
	case err := <-done:
		assert.Assert(t, err != nil)
	case <-time.After(10 * time.Second):
		assert.Assert(t, false, "checkReadiness kept waiting after its context ended")
	}
}

// waitForReady gives up as soon as the service exits after it or its caller
// has seen the service running, or as soon as the caller's child exits while
// no service is running. It keeps waiting while the caller's child is still
// starting, because the child writes its PID file only some time after Start
// returns.
func TestWaitForReadyStopsWhenTheServiceExits(t *testing.T) {
	noConfig := func() (*rest.Config, error) { return nil, errors.New("control plane is not running") }
	childRunning := make(chan struct{})
	exited := make(chan struct{})
	close(exited)
	tests := []struct {
		name        string
		childExited <-chan struct{}
		running     []bool
		wantTimeout bool
	}{
		{"exits after running", nil, []bool{true, true, false}, false},
		{"exits before the first check", nil, []bool{false}, false},
		{"child still starting", childRunning, []bool{false}, true},
		{"child exits before writing its PID file", exited, []bool{false}, false},
		{"child exits while another serve runs", exited, []bool{true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			running := func() bool {
				r := tt.running[min(calls, len(tt.running)-1)]
				calls++
				return r
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := waitForReady(ctx, time.Now(), tt.childExited, running, noConfig)
			if tt.wantTimeout {
				assert.ErrorIs(t, err, context.DeadlineExceeded)
			} else {
				assert.NilError(t, ctx.Err(), "waitForReady waited until the deadline")
				assert.ErrorContains(t, err, "exited before it became ready")
			}
		})
	}
}
