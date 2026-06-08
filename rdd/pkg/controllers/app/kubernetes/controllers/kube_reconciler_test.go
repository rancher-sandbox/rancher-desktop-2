// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// fakeK3sServer starts an httptest TLS server that answers /healthz with
// healthzStatus and writes a matching k3s-shaped kubeconfig to srcPath.
// Returns the configured kubeconfig path and registers t.Cleanup for the
// server.
func fakeK3sServer(t *testing.T, srcPath string, healthzStatus int) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/healthz" {
			w.WriteHeader(healthzStatus)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["default"] = &clientcmdapi.Cluster{
		Server:                   srv.URL,
		CertificateAuthorityData: certPEM,
	}
	cfg.AuthInfos["default"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: certPEM,
		ClientKeyData:         clientKeyPEM(t, srv),
	}
	cfg.Contexts["default"] = &clientcmdapi.Context{
		Cluster:  "default",
		AuthInfo: "default",
	}
	cfg.CurrentContext = "default"

	assert.NilError(t, clientcmd.WriteToFile(*cfg, srcPath))
	return srcPath
}

// clientKeyPEM extracts the test server's private key in PEM form.
// httptest's self-signed cert ships with its own key, which probeK3sAPI
// requires when the kubeconfig declares ClientCertificateData / ClientKeyData.
func clientKeyPEM(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	leaf := srv.TLS.Certificates[0]
	keyBytes, err := x509.MarshalPKCS8PrivateKey(leaf.PrivateKey)
	assert.NilError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
}

// newAppRunning builds an App with Kubernetes enabled and the Running condition
// set to True so Reconcile reaches the probe path.
func newAppRunning() *appv1alpha1.App {
	return &appv1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Generation: 1},
		Spec: appv1alpha1.AppSpec{
			Running:    true,
			Kubernetes: appv1alpha1.KubernetesSpec{Enabled: true, Version: "1.32.0"},
		},
		Status: appv1alpha1.AppStatus{
			Conditions: []metav1.Condition{{
				Type:   appv1alpha1.AppConditionRunning,
				Status: metav1.ConditionTrue,
				Reason: "Started",
			}},
		},
	}
}

// newAppRunningKubeReady extends newAppRunning with a pre-existing
// KubernetesReady=True condition, simulating an App that has already
// observed a healthy k3s probe.
func newAppRunningKubeReady() *appv1alpha1.App {
	app := newAppRunning()
	app.Status.Conditions = append(app.Status.Conditions, metav1.Condition{
		Type:   appv1alpha1.AppConditionKubernetesReady,
		Status: metav1.ConditionTrue,
		Reason: appv1alpha1.AppKubernetesReasonReady,
	})
	return app
}

// newAppKubernetesDisabled builds an App with Kubernetes disabled and the
// Running condition True, so Reconcile takes the NotApplicable branch.
func newAppKubernetesDisabled() *appv1alpha1.App {
	app := newAppRunning()
	app.Spec.Kubernetes.Enabled = false
	return app
}

// newAppNotRunning builds an App with Kubernetes enabled but the Running
// condition False, so Reconcile takes the NotRunning branch.
func newAppNotRunning() *appv1alpha1.App {
	app := newAppRunning()
	app.Status.Conditions[0].Status = metav1.ConditionFalse
	app.Status.Conditions[0].Reason = "Stopped"
	return app
}

// isolateKubeconfig points HOME and KUBECONFIG at dir so the reconciler's
// kubeconfig writes cannot touch the developer's real ~/.kube/config even
// if a future refactor changes KUBECONFIG-precedence handling.
func isolateKubeconfig(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("KUBECONFIG", filepath.Join(dir, ".kube", "config"))
}

func newKubeScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	assert.NilError(t, appv1alpha1.AddToScheme(s))
	return s
}

// findKubeReady returns the KubernetesReady condition on app, or nil if absent.
func findKubeReady(app *appv1alpha1.App) *metav1.Condition {
	return apimeta.FindStatusCondition(app.Status.Conditions, appv1alpha1.AppConditionKubernetesReady)
}

func Test_Reconcile_KubernetesReady_Ready(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}
	// removeKubeContext cancels the in-flight current-context probe and waits
	// for the goroutine started by manageKubeContext to finish, so it cannot
	// still be reading tempdir state after the test returns.
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeHealthyRequeue,
		"Ready path should requeue after kubeHealthyRequeue so a later k3s death surfaces")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionTrue)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonReady)
}

func Test_Reconcile_InstanceKubeConfig_WrittenOnReady_RemovedOnDisable(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	instanceKubeConfig := filepath.Join(dir, "kube.config")
	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: instanceKubeConfig,
	}
	// The healthy reconcile starts a current-context probe goroutine; ensure it
	// finishes before the test returns even if an assertion below fails.
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}

	// A healthy reconcile publishes the standalone kubeconfig that rdd run reads.
	_, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	_, err = os.Stat(instanceKubeConfig)
	assert.NilError(t, err, "instance kubeconfig should exist after a healthy reconcile")

	// Disabling Kubernetes removes it again, leaving nothing for rdd run to find.
	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	got.Spec.Kubernetes.Enabled = false
	assert.NilError(t, c.Update(context.Background(), got))

	_, err = r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	_, err = os.Stat(instanceKubeConfig)
	assert.Assert(t, os.IsNotExist(err),
		"instance kubeconfig should be removed when Kubernetes is disabled")
}

func Test_Reconcile_KubernetesReady_MergeFailed(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)

	// Plant a regular file where the destination kubeconfig's parent
	// directory should be. createReplaceKubeContext calls
	// os.MkdirAll("$HOME/.kube"), which fails with ENOTDIR.
	kubeDir := filepath.Join(dir, ".kube")
	assert.NilError(t, os.WriteFile(kubeDir, []byte("not a directory"), 0o600))
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"MergeFailed path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonMergeFailed)
	assert.Assert(t, cond.Message != "",
		"MergeFailed condition should carry the underlying error in its message")
}

func Test_Reconcile_KubernetesReady_NotApplicable(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppKubernetesDisabled()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{Client: c}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Duration(0),
		"NotApplicable path should not request a requeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonNotApplicable)
}

func Test_Reconcile_KubernetesReady_NotRunning(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppNotRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{Client: c}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, time.Duration(0),
		"NotRunning path should not request a requeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonNotRunning)
}

func Test_Reconcile_KubernetesReady_Probing_ProbeError(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	// Point K3sConfigPath at a file that does not exist so probeK3sAPI
	// returns the (false, err) "kubeconfig unreadable" path.
	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          filepath.Join(dir, "missing-k3s.yaml"),
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"Probing path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

func Test_Reconcile_KubernetesReady_Probing_Unhealthy(t *testing.T) {
	dir := t.TempDir()
	// fakeK3sServer answers /healthz with 500, so probeK3sAPI takes the
	// probeUnhealthy "server reachable but said no" path.
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusInternalServerError)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunning()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
		"Probing path should requeue after kubeProbeRequeue")

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))

	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

// Test_Reconcile_KubernetesReady_Unhealthy_FromReady_ImmediateFlip confirms
// that a 5xx from k3s flips an already-Ready condition to Probing without
// being smoothed by the ambiguous-failure counter. 5xx is k3s deliberately
// reporting "not ready," not transient noise.
func Test_Reconcile_KubernetesReady_Unhealthy_FromReady_ImmediateFlip(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusInternalServerError)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunningKubeReady()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	_, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

// Test_Reconcile_KubernetesReady_Unreachable_ImmediateProbing confirms that a
// connection-refused-class error flips to Probing immediately, with no
// smoothing. Uses probeFn injection because httptest can't easily produce
// ECONNREFUSED in a portable way.
func Test_Reconcile_KubernetesReady_Unreachable_ImmediateProbing(t *testing.T) {
	scheme := newKubeScheme(t)
	app := newAppRunningKubeReady()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:  c,
		probeFn: func(context.Context) (probeResult, error) { return probeUnreachable, nil },
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue)

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

// Test_Reconcile_KubernetesReady_Ambiguous_TolerateWhileReady confirms that
// ambiguous probe failures below the threshold keep an already-Ready
// condition stable instead of immediately flipping to Probing.
func Test_Reconcile_KubernetesReady_Ambiguous_TolerateWhileReady(t *testing.T) {
	scheme := newKubeScheme(t)
	app := newAppRunningKubeReady()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:  c,
		probeFn: func(context.Context) (probeResult, error) { return probeAmbiguous, nil },
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	for i := 1; i < probeFailureThreshold; i++ {
		result, err := r.Reconcile(context.Background(), req)
		assert.NilError(t, err)
		assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
			"ambiguous failure %d should requeue at kubeProbeRequeue", i)

		got := &appv1alpha1.App{}
		assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
		cond := findKubeReady(got)
		assert.Assert(t, cond != nil, "KubernetesReady condition missing after failure %d", i)
		assert.Equal(t, cond.Status, metav1.ConditionTrue,
			"condition should stay True until the threshold is reached (failure %d)", i)
	}
}

// Test_Reconcile_KubernetesReady_Ambiguous_ReachThresholdFlipsToProbing
// confirms that a streak of ambiguous failures crossing the threshold flips
// the condition from Ready to Probing.
func Test_Reconcile_KubernetesReady_Ambiguous_ReachThresholdFlipsToProbing(t *testing.T) {
	scheme := newKubeScheme(t)
	app := newAppRunningKubeReady()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:  c,
		probeFn: func(context.Context) (probeResult, error) { return probeAmbiguous, nil },
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	for i := 1; i <= probeFailureThreshold; i++ {
		_, err := r.Reconcile(context.Background(), req)
		assert.NilError(t, err)
	}

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

// Test_Reconcile_KubernetesReady_Ambiguous_HealthyResetsCounter confirms that
// a successful probe resets the streak counter: after one healthy reconcile,
// the next ambiguous failure starts counting from 1 again rather than
// continuing the previous streak.
func Test_Reconcile_KubernetesReady_Ambiguous_HealthyResetsCounter(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)
	isolateKubeconfig(t, dir)

	scheme := newKubeScheme(t)
	app := newAppRunningKubeReady()
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	// Switch probeFn between Ambiguous and the real probeK3sAPI (Healthy) to
	// simulate a brief slow patch followed by recovery.
	step := 0
	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}
	r.probeFn = func(context.Context) (probeResult, error) {
		step++
		switch {
		case step < probeFailureThreshold:
			return probeAmbiguous, nil
		case step == probeFailureThreshold:
			return probeHealthy, nil
		default:
			return probeAmbiguous, nil
		}
	}
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	// Run ambiguous streak just under threshold, then a healthy reconcile.
	for range probeFailureThreshold {
		_, err := r.Reconcile(context.Background(), req)
		assert.NilError(t, err)
	}
	// Now ambiguous failures should start counting from zero again: the next
	// probeFailureThreshold-1 reconciles should keep the condition Ready.
	for i := 1; i < probeFailureThreshold; i++ {
		_, err := r.Reconcile(context.Background(), req)
		assert.NilError(t, err)

		got := &appv1alpha1.App{}
		assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
		cond := findKubeReady(got)
		assert.Assert(t, cond != nil)
		assert.Equal(t, cond.Status, metav1.ConditionTrue,
			"counter should have reset on the healthy probe; ambiguous failure %d should still be tolerated", i)
	}
}

// Test_Reconcile_KubernetesReady_Ambiguous_DuringStartupIsImmediate confirms
// that ambiguous failures during startup (no prior Ready condition) flip to
// Probing immediately, without waiting for the threshold. The smoothing only
// applies to transitions away from Ready.
func Test_Reconcile_KubernetesReady_Ambiguous_DuringStartupIsImmediate(t *testing.T) {
	scheme := newKubeScheme(t)
	app := newAppRunning() // no KubernetesReady condition yet
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app).
		WithStatusSubresource(app).
		Build()

	r := &KubernetesReconciler{
		Client:  c,
		probeFn: func(context.Context) (probeResult, error) { return probeAmbiguous, nil },
	}

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: appName}}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeProbeRequeue)

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil)
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonProbing)
}

func Test_classifyProbeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want probeResult
	}{
		{"context deadline", context.DeadlineExceeded, probeAmbiguous},
		{"wrapped deadline", fmt.Errorf("get https://x/healthz: %w", context.DeadlineExceeded), probeAmbiguous},
		{"syscall timeout", syscall.ETIMEDOUT, probeAmbiguous},
		{"connection refused", syscall.ECONNREFUSED, probeUnreachable},
		{"connection reset", syscall.ECONNRESET, probeUnreachable},
		{"plain error", errors.New("dns lookup failed"), probeUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, classifyProbeError(tc.err), tc.want)
		})
	}
}
