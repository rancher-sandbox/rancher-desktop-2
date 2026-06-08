// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package controllers

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestControllerManagerDiscoveryGroup(t *testing.T) {
	const passthroughPort = 4321

	env := &envtest.Environment{
		DownloadBinaryAssets: true,
	}
	cfg, err := env.Start()
	assert.NilError(t, err, "failed to start environment")

	defer func() {
		err := env.Stop()
		// On Windows, `env.Stop()` will return an error because it can't kill
		// etcd gracefully; this is not an issue for this test.
		// Also, in CI only, ignore failure to stop kube-apiserver.
		if runtime.GOOS != "windows" && err != nil {
			checkError := os.Getenv("CI") == ""
			checkError = checkError || !strings.Contains(err.Error(), "timeout waiting for process kube-apiserver to stop")
			if checkError {
				assert.NilError(t, err, "failed to stop environment")
			}
		}
	}()

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthServer.Close()
	port := healthServer.Listener.Addr().(*net.TCPAddr).Port

	client, err := kubernetes.NewForConfig(cfg)
	assert.NilError(t, err, "failed to create kubernetes client")

	d1, err := NewControllerManagerDiscoveryGroup(cfg, "test-group")
	assert.NilError(t, err, "failed to create ControllerManagerDiscoveryGroup")

	// Register a controller manager.
	assert.NilError(t, d1.RegisterControllerManager(t.Context(), ControllerManagerInput{
		HealthPort:         1234,
		MetricsPort:        5678,
		PassthroughPort:    passthroughPort,
		EnabledControllers: nil,
	}), "failed to register controller manager")

	// Check that the config map exists.
	cm, err := client.CoreV1().ConfigMaps(d1.namespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map")
	assert.Assert(t, cmp.Len(cm.Data, 1), "expected config map to have one entry")
	assert.Check(t, cmp.Contains(cm.Data, d1.name))

	// Check that we can read it back out.
	info, err := d1.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "failed to discover controller manager")
	assert.DeepEqual(t, &ControllerManagerInfo{
		ControllerManagerInput: ControllerManagerInput{
			HealthPort:         1234,
			MetricsPort:        5678,
			EnabledControllers: nil,
		},
		StartTime:           info.StartTime,
		HealthEndpoint:      info.HealthEndpoint,
		MetricsEndpoint:     info.MetricsEndpoint,
		PassthroughEndpoint: fmt.Sprintf("http://localhost:%d/", passthroughPort),
	}, info)

	controllers, err := d1.GetEnabledControllers(t.Context())
	assert.NilError(t, err, "failed to get enabled controllers")
	assert.Check(t, cmp.Len(controllers, 0), "expected no enabled controllers")

	running, _, err := d1.IsControllerRunning(t.Context(), "hello")
	assert.NilError(t, err, "failed to check if controller is running")
	assert.Check(t, !running, "expected controller not to be running")

	// Register a second controller manager to test unregister.
	d2, err := NewControllerManagerDiscoveryGroup(cfg, "test-group-2")
	assert.NilError(t, err, "failed to create second ControllerManagerDiscoveryGroup")
	assert.NilError(t, d2.RegisterControllerManager(t.Context(), ControllerManagerInput{
		HealthPort:          port,
		MetricsPort:         8765,
		EnabledControllers:  []string{"hello"},
		PassthroughPort:     passthroughPort,
		EnabledPassthroughs: map[string][]string{"hello": {"foo", "bar"}},
	}), "failed to register second controller manager")

	// Check that the config map is updated.
	cm, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map after second registration")
	assert.Assert(t, cmp.Len(cm.Data, 2), "expected config map to have two entries after second registration")
	assert.Check(t, cmp.Contains(cm.Data, d1.name))
	assert.Check(t, cmp.Contains(cm.Data, d2.name))

	// Check that we can read the second one back out.
	info, err = d2.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "failed to discover second controller manager")
	assert.DeepEqual(t, &ControllerManagerInfo{
		ControllerManagerInput: ControllerManagerInput{
			HealthPort:          port,
			MetricsPort:         8765,
			EnabledControllers:  []string{"hello"},
			EnabledPassthroughs: map[string][]string{"hello": {"foo", "bar"}},
		},
		StartTime:           info.StartTime,
		HealthEndpoint:      info.HealthEndpoint,
		MetricsEndpoint:     info.MetricsEndpoint,
		PassthroughEndpoint: fmt.Sprintf("http://localhost:%d/", passthroughPort),
	}, info)

	// Check that we can get controllers.
	controllers, err = d2.GetEnabledControllers(t.Context())
	assert.NilError(t, err, "failed to get enabled controllers")
	assert.DeepEqual(t, []string{"hello"}, controllers)

	running, _, err = d2.IsControllerRunning(t.Context(), "hello")
	assert.NilError(t, err, "failed to check if controller is running")
	assert.Check(t, running, "expected controller to be running")

	// Unregister the first controller manager.
	assert.NilError(t, d1.UnregisterControllerManager(t.Context()), "failed to unregister first controller manager")

	// Check that discovering the first controller manager now fails.
	info, err = d1.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "unexpected error discovering unregistered controller manager")
	assert.Assert(t, info == nil, "expected nil info for unregistered controller manager")

	// Check that the second controller manager is still discoverable.
	info, err = d2.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "failed to discover second controller manager after first unregistered")
	assert.Check(t, info != nil, "expected non-nil info for second controller manager")

	// Check that the config map still exists with one entry.
	cm, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map after first unregistered")
	assert.Assert(t, cmp.Len(cm.Data, 1), "expected config map to have one entry after first unregistered")
	assert.Check(t, cmp.Contains(cm.Data, d2.name))
	assert.Check(t, cm.Data[d1.name] == "", "expected first controller manager entry to be removed")

	// LookupPassthroughEndpoint should return the endpoint for enabled passthroughs
	endpoint, err := d2.LookupPassthroughEndpoint(t.Context(), "hello", "foo")
	assert.NilError(t, err, "failed to lookup passthrough endpoint")
	assert.Check(t, endpoint != "", "expected non-empty endpoint for enabled passthrough")
	assert.Equal(t, endpoint, info.PassthroughEndpoint, "expected endpoint to match PassthroughEndpoint")

	// Should return empty string for non-existent passthrough
	endpoint, err = d2.LookupPassthroughEndpoint(t.Context(), "hello", "notfound")
	assert.NilError(t, err, "failed to lookup non-existent passthrough endpoint")
	assert.Equal(t, endpoint, "", "expected empty endpoint for non-existent passthrough")

	// Should return endpoint for another enabled passthrough
	endpoint, err = d2.LookupPassthroughEndpoint(t.Context(), "hello", "bar")
	assert.NilError(t, err, "failed to lookup second passthrough endpoint")
	assert.Equal(t, endpoint, info.PassthroughEndpoint, "expected endpoint to match PassthroughEndpoint for 'bar'")

	// Should not return endpoint for the wrong controller
	endpoint, err = d2.LookupPassthroughEndpoint(t.Context(), "another", "foo")
	assert.NilError(t, err, "failed to lookup second passthrough endpoint")
	assert.Equal(t, endpoint, "", "expected empty endpoint for wrong controller")

	// Unregister the second controller manager.
	assert.NilError(t, d2.UnregisterControllerManager(t.Context()), "failed to unregister second controller manager")

	// The control plane owns the ConfigMap; it must survive all controller
	// managers unregistering or --since=startup breaks.
	cm, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "expected config map to still exist after all controller managers unregistered")
	assert.Assert(t, cmp.Len(cm.Data, 0), "expected config map to have no data entries after all controller managers unregistered")
}

func TestInitDiscoveryAndMarkReady(t *testing.T) {
	env := &envtest.Environment{
		DownloadBinaryAssets: true,
	}
	cfg, err := env.Start()
	assert.NilError(t, err, "failed to start environment")

	defer func() {
		err := env.Stop()
		if runtime.GOOS != "windows" && err != nil {
			checkError := os.Getenv("CI") == ""
			checkError = checkError || !strings.Contains(err.Error(), "timeout waiting for process kube-apiserver to stop")
			if checkError {
				assert.NilError(t, err, "failed to stop environment")
			}
		}
	}()

	client, err := kubernetes.NewForConfig(cfg)
	assert.NilError(t, err, "failed to create kubernetes client")

	// InitDiscovery creates the ConfigMap without the ready annotation.
	assert.NilError(t, InitDiscovery(t.Context(), client), "failed to init discovery")

	cm, err := client.CoreV1().ConfigMaps(RDDSystemNamespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get discovery config map after init")
	assert.Check(t, cm.Annotations[ReadyAnnotation] == "", "expected ready annotation to be unset after InitDiscovery")

	// MarkControlPlaneReady sets the annotation on the existing ConfigMap.
	assert.NilError(t, MarkControlPlaneReady(t.Context(), client), "failed to mark control plane ready")

	cm, err = client.CoreV1().ConfigMaps(RDDSystemNamespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get discovery config map after mark ready")
	assert.Equal(t, cm.Annotations[ReadyAnnotation], "true", "expected ready annotation to be set")

	// Registering a controller manager preserves the ready annotation.
	d, err := NewControllerManagerDiscoveryGroup(cfg, "test-group")
	assert.NilError(t, err, "failed to create ControllerManagerDiscoveryGroup")
	assert.NilError(t, d.RegisterControllerManager(t.Context(), ControllerManagerInput{
		HealthPort: 1234,
	}), "failed to register controller manager")

	cm, err = client.CoreV1().ConfigMaps(RDDSystemNamespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get discovery config map after register")
	assert.Equal(t, cm.Annotations[ReadyAnnotation], "true", "expected ready annotation to survive controller manager registration")
	firstUID := cm.UID

	// Re-init replaces the ConfigMap so --since=startup sees a fresh
	// creationTimestamp. UID is time-independent; creationTimestamp has second precision.
	assert.NilError(t, InitDiscovery(t.Context(), client), "failed to re-init discovery")
	cm, err = client.CoreV1().ConfigMaps(RDDSystemNamespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get discovery config map after re-init")
	assert.Check(t, cm.Annotations[ReadyAnnotation] == "", "expected ready annotation to be cleared by re-init")
	assert.Check(t, cm.UID != firstUID, "expected re-init to create a fresh ConfigMap with a new UID")
}
