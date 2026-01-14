// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package controllers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestControllerManagerDiscoveryGroup(t *testing.T) {
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
	assert.NilError(t, d1.RegisterControllerManager(t.Context(), 1234, 5678, nil), "failed to register controller manager")

	// Check that the config map exists.
	cm, err := client.CoreV1().ConfigMaps(d1.namespace).Get(t.Context(), controllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map")
	assert.Assert(t, cmp.Len(cm.Data, 1), "expected config map to have one entry")
	assert.Check(t, cmp.Contains(cm.Data, d1.name))

	// Check that we can read it back out.
	info, err := d1.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "failed to discover controller manager")
	assert.DeepEqual(t, &ControllerManagerInfo{
		HealthPort:         1234,
		MetricsPort:        5678,
		EnabledControllers: nil,
		StartTime:          info.StartTime,
		HealthEndpoint:     info.HealthEndpoint,
		MetricsEndpoint:    info.MetricsEndpoint,
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
	assert.NilError(t, d2.RegisterControllerManager(t.Context(), port, 8765, []string{"hello"}), "failed to register second controller manager")

	// Check that the config map is updated.
	cm, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), controllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map after second registration")
	assert.Assert(t, cmp.Len(cm.Data, 2), "expected config map to have two entries after second registration")
	assert.Check(t, cmp.Contains(cm.Data, d1.name))
	assert.Check(t, cmp.Contains(cm.Data, d2.name))

	// Check that we can read the second one back out.
	info, err = d2.DiscoverControllerManager(t.Context())
	assert.NilError(t, err, "failed to discover second controller manager")
	assert.DeepEqual(t, &ControllerManagerInfo{
		HealthPort:         port,
		MetricsPort:        8765,
		EnabledControllers: []string{"hello"},
		StartTime:          info.StartTime,
		HealthEndpoint:     info.HealthEndpoint,
		MetricsEndpoint:    info.MetricsEndpoint,
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
	cm, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), controllerManagerConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err, "failed to get controller manager config map after first unregistered")
	assert.Assert(t, cmp.Len(cm.Data, 1), "expected config map to have one entry after first unregistered")
	assert.Check(t, cmp.Contains(cm.Data, d2.name))
	assert.Check(t, cm.Data[d1.name] == "", "expected first controller manager entry to be removed")

	// Unregister the second controller manager.
	assert.NilError(t, d2.UnregisterControllerManager(t.Context()), "failed to unregister second controller manager")

	// Check that the config map is removed.
	_, err = client.CoreV1().ConfigMaps(d2.namespace).Get(t.Context(), controllerManagerConfigMapName, metav1.GetOptions{})
	assert.Assert(t, errors.IsNotFound(err), "expected config map to be deleted after all controller managers unregistered")
}
