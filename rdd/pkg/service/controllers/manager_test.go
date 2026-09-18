// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package controllers

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// probeWebhookManager blocks in Setup until the test releases it.
type probeWebhookManager struct {
	setupStarted chan struct{}
	releaseSetup chan struct{}
}

func (m *probeWebhookManager) GetConfigName() string            { return "probe-webhook" }
func (m *probeWebhookManager) GetWebhookType() base.WebhookType { return base.ValidatingWebhook }
func (m *probeWebhookManager) Setup() error {
	close(m.setupStarted)
	<-m.releaseSetup
	return nil
}

// probeController is a webhook controller with no CRD and no reconciler.
type probeController struct {
	webhookPort    int
	webhookManager *probeWebhookManager
}

func (c *probeController) GetName() string { return "probe" }

func (c *probeController) GetAPIGroup() string                                     { return "probe.test" }
func (c *probeController) GetCRDData() string                                      { return "" }
func (c *probeController) RegisterWithManager(context.Context, ctrl.Manager) error { return nil }
func (c *probeController) SetWebhookPort(port int)                                 { c.webhookPort = port }

func (c *probeController) GetWebhookServiceName() string { return "probe-webhook-service" }

func (c *probeController) GetWebhookManagers() []base.WebhookManager {
	return []base.WebhookManager{c.webhookManager}
}

func TestSharedControllerManagerMarksReadyAfterWebhooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("RDD_INSTANCE", "test-webhook-startup-order")

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
	assert.NilError(t, InitDiscovery(t.Context(), client), "failed to init discovery")

	scm, err := NewSharedControllerManager("test", cfg, 18080, 18081)
	assert.NilError(t, err, "failed to create shared controller manager")
	webhookManager := &probeWebhookManager{
		setupStarted: make(chan struct{}),
		releaseSetup: make(chan struct{}),
	}
	assert.NilError(t, scm.RegisterController(&probeController{webhookManager: webhookManager}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	startErr := make(chan error, 1)
	go func() { startErr <- scm.Start(ctx) }()

	select {
	case <-webhookManager.setupStarted:
	case err := <-startErr:
		assert.Assert(t, false, "Start returned before webhook setup: %v", err)
	case <-time.After(60 * time.Second):
		assert.Assert(t, false, "timed out waiting for webhook setup to start")
	}

	readyAnnotation := func() string {
		cm, err := client.CoreV1().ConfigMaps(RDDSystemNamespace).Get(t.Context(), ControllerManagerConfigMapName, metav1.GetOptions{})
		assert.NilError(t, err, "failed to get discovery config map")
		return cm.Annotations[ReadyAnnotation]
	}
	assert.Equal(t, readyAnnotation(), "", "control plane marked ready before its webhook configurations were applied")

	close(webhookManager.releaseSetup)
	deadline := time.Now().Add(30 * time.Second)
	for readyAnnotation() != "true" {
		assert.Assert(t, time.Now().Before(deadline), "timed out waiting for the control plane to be marked ready")
		time.Sleep(100 * time.Millisecond)
	}

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(scm.GetWebhookPort()))
	dialer := &tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // the test only checks that the server answers
	conn, err := dialer.DialContext(t.Context(), "tcp", addr)
	assert.NilError(t, err, "webhook server not answering after the control plane was marked ready")
	assert.NilError(t, conn.Close())

	cancel()
	select {
	case err := <-startErr:
		assert.NilError(t, err, "Start returned an error on shutdown")
	case <-time.After(30 * time.Second):
		assert.Assert(t, false, "timed out waiting for Start to return")
	}
}
