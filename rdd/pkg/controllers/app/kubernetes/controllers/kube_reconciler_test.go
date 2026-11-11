// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/poll"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
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
	return writeK3sKubeconfig(t, srv, srcPath)
}

// writeK3sKubeconfig writes a k3s-shaped kubeconfig for srv to srcPath,
// trusting the server's certificate and reusing it as the client cert.
// Returns srcPath.
func writeK3sKubeconfig(t *testing.T, srv *httptest.Server, srcPath string) string {
	t.Helper()
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
		Name: appName, Generation: 1,
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

// readyNode returns a Node carrying a Ready condition set to True.
func readyNode() *corev1.Node {
	return &corev1.Node{
		Name: "lima-rd",
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
}

// notReadyNode returns a registered Node whose Ready condition is False.
func notReadyNode() *corev1.Node {
	node := readyNode()
	node.Status.Conditions[0].Status = corev1.ConditionFalse
	return node
}

// fakeClientset returns a clientsetFn that serves objs from an in-memory
// client, letting the node check run without a real cluster.
func fakeClientset(objs ...k8sruntime.Object) func() (kubernetes.Interface, error) {
	return func() (kubernetes.Interface, error) {
		return k8sfake.NewSimpleClientset(objs...), nil
	}
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
		clientsetFn:            fakeClientset(readyNode()),
	}
	// removeKubeContext cancels the in-flight current-context probe and waits
	// for the goroutine started by checkCurrentKubeContext to finish, so it
	// cannot still be reading tempdir state after the test returns.
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{Name: appName}
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

// Test_Reconcile_KubernetesReady_GatedUntilNodeReady checks that a healthy
// API server does not promote KubernetesReady to True until the node is
// Ready, so `rdd set` cannot return while a scheduled pod would have no
// node to run on.
func Test_Reconcile_KubernetesReady_GatedUntilNodeReady(t *testing.T) {
	tests := []struct {
		name       string
		objects    []k8sruntime.Object
		wantReason string
	}{
		{
			name:       "node not Ready",
			objects:    []k8sruntime.Object{notReadyNode()},
			wantReason: appv1alpha1.AppKubernetesReasonWaitingForNode,
		},
		{
			name:       "no node registered",
			objects:    []k8sruntime.Object{},
			wantReason: appv1alpha1.AppKubernetesReasonWaitingForNode,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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
				clientsetFn:            fakeClientset(tc.objects...),
			}
			t.Cleanup(func() { r.removeKubeContext(context.Background()) })

			req := ctrl.Request{Name: appName}
			result, err := r.Reconcile(context.Background(), req)
			assert.NilError(t, err)
			assert.Equal(t, result.RequeueAfter, kubeProbeRequeue,
				"a not-yet-usable cluster should requeue after kubeProbeRequeue")

			got := &appv1alpha1.App{}
			assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
			cond := findKubeReady(got)
			assert.Assert(t, cond != nil, "KubernetesReady condition missing")
			assert.Equal(t, cond.Status, metav1.ConditionFalse,
				"KubernetesReady must stay False until a node is Ready")
			assert.Equal(t, cond.Reason, tc.wantReason)
		})
	}
}

// Test_Reconcile_KubernetesReady_AnyReadyNodeSuffices checks that one Ready
// node promotes the condition even while another node is not Ready: a
// scheduled workload has somewhere to run as soon as any node is Ready.
func Test_Reconcile_KubernetesReady_AnyReadyNodeSuffices(t *testing.T) {
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

	secondNode := notReadyNode()
	secondNode.Name = "lima-rd-2"
	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
		clientsetFn:            fakeClientset(readyNode(), secondNode),
	}
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{Name: appName}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeHealthyRequeue)

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionTrue,
		"one Ready node should be enough; a not-Ready sibling must not block")
	assert.Equal(t, cond.Reason, appv1alpha1.AppKubernetesReasonReady)
}

// Test_Reconcile_KubernetesReady_StaysReadyDespiteNodeBlip checks that the
// node gate runs only on the path to the first Ready. Once Ready,
// API-server liveness alone keeps the condition True, so a transient node
// blip cannot flap Settled.
func Test_Reconcile_KubernetesReady_StaysReadyDespiteNodeBlip(t *testing.T) {
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

	r := &KubernetesReconciler{
		Client:                 c,
		K3sConfigPath:          srcPath,
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
		// No Ready node: if the gate ran here it would flip the
		// condition to False, so leaving it True proves the gate is skipped.
		clientsetFn: fakeClientset(),
	}
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{Name: appName}
	result, err := r.Reconcile(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.RequeueAfter, kubeHealthyRequeue)

	got := &appv1alpha1.App{}
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, got))
	cond := findKubeReady(got)
	assert.Assert(t, cond != nil, "KubernetesReady condition missing")
	assert.Equal(t, cond.Status, metav1.ConditionTrue,
		"already-Ready condition must not flap on a node blip")
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
		clientsetFn:            fakeClientset(readyNode()),
	}
	// The healthy reconcile starts a current-context probe goroutine; ensure it
	// finishes before the test returns even if an assertion below fails.
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	req := ctrl.Request{Name: appName}

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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

	req := ctrl.Request{Name: appName}
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

// Test_RemoveKubeContext_WaitsForProbeWrite pins the ordering between the probe
// goroutine and removeKubeContext. Once the probe is past its cancellation guard,
// removeKubeContext must not return until the probe's write lands, or it clears a
// current-context that the probe then restores.
func Test_RemoveKubeContext_WaitsForProbeWrite(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)

	r := &KubernetesReconciler{
		K3sConfigPath:          makeSrcKubeconfig(t, dir),
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}

	inWrite := make(chan struct{})
	release := make(chan struct{})
	releaseProbe := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseProbe)
	testBeforeSetCurrentKubeContext = func() {
		close(inWrite)
		<-release
	}
	t.Cleanup(func() { testBeforeSetCurrentKubeContext = nil })

	// The kubeconfig has no current-context, so the probe reports it unhealthy
	// and goes on to write one, parking in the seam above.
	assert.NilError(t, r.mergeKubeContext(context.Background()))
	r.checkCurrentKubeContext(context.Background())
	select {
	case <-inWrite:
	case <-time.After(10 * time.Second):
		assert.Assert(t, false, "probe never reached the current-context write")
	}

	removed := make(chan struct{})
	go func() {
		r.removeKubeContext(context.Background())
		close(removed)
	}()

	select {
	case <-removed:
		assert.Assert(t, false,
			"removeKubeContext returned while the probe's kubeconfig write was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	releaseProbe()
	<-removed

	cfg := loadDest(t, filepath.Join(dir, ".kube", "config"))
	assert.Equal(t, cfg.CurrentContext, "", "probe restored a current-context that removeKubeContext cleared")
	_, found := cfg.Contexts[instance.Name()]
	assert.Assert(t, !found, "removeKubeContext should have deleted the context")
}

// Test_Probes_CloseConnections checks that both healthz probes close their
// connections, because the reconciler calls probeK3sAPI for as long as
// Kubernetes runs and probeCurrentKubeContext each time KubernetesReady turns
// True.
func Test_Probes_CloseConnections(t *testing.T) {
	probes := map[string]func(t *testing.T, srcPath string){
		"probeK3sAPI": func(t *testing.T, srcPath string) {
			r := &KubernetesReconciler{K3sConfigPath: srcPath}
			result, err := r.probeK3sAPI(t.Context())
			assert.NilError(t, err)
			assert.Equal(t, result, probeHealthy)
		},
		"probeCurrentKubeContext": func(t *testing.T, srcPath string) {
			// The probe treats a context named "default" as unhealthy.
			cfg := loadDest(t, srcPath)
			cfg.Contexts["k3s"] = cfg.Contexts["default"]
			delete(cfg.Contexts, "default")
			cfg.CurrentContext = "k3s"
			destPath := filepath.Join(filepath.Dir(srcPath), "config")
			assert.NilError(t, clientcmd.WriteToFile(*cfg, destPath))
			t.Setenv("KUBECONFIG", destPath)
			assert.Assert(t, probeCurrentKubeContext(t.Context(), "k3s"))
		},
	}
	for name, probe := range probes {
		t.Run(name, func(t *testing.T) {
			var open atomic.Int64
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				switch state {
				case http.StateNew:
					open.Add(1)
				case http.StateClosed, http.StateHijacked:
					open.Add(-1)
				}
			}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			srcPath := writeK3sKubeconfig(t, srv, filepath.Join(t.TempDir(), "k3s.yaml"))

			for range 3 {
				probe(t, srcPath)
			}
			poll.WaitOn(t, func(poll.LogT) poll.Result {
				if n := open.Load(); n != 0 {
					return poll.Continue("%d connections still open", n)
				}
				return poll.Success()
			}, poll.WithTimeout(2*time.Second))
		})
	}
}

// writeContextKubeconfig writes destPath with a single context named name
// whose cluster points at server (trusting caPEM when non-nil) and makes it
// current.
func writeContextKubeconfig(t *testing.T, destPath, name, server string, caPEM []byte) {
	t.Helper()
	writeClusterKubeconfig(t, destPath, name, &clientcmdapi.Cluster{Server: server, CertificateAuthorityData: caPEM})
}

// writeClusterKubeconfig writes destPath with a single context named name
// that uses cluster, and makes it current.
func writeClusterKubeconfig(t *testing.T, destPath, name string, cluster *clientcmdapi.Cluster) {
	t.Helper()
	assert.NilError(t, os.MkdirAll(filepath.Dir(destPath), 0o700))
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[name] = cluster
	cfg.Contexts[name] = &clientcmdapi.Context{Cluster: name}
	cfg.CurrentContext = name
	assert.NilError(t, clientcmd.WriteToFile(*cfg, destPath))
}

// serverCAPEM returns srv's certificate in PEM form, for a kubeconfig that
// should trust it.
func serverCAPEM(t *testing.T, srv *httptest.Server) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

// httpsConnectProxy starts an https proxy that tunnels each CONNECT to backend,
// or answers it with 502 Bad Gateway when backend is "". It serves httptest's
// built-in certificate, which the probe cannot verify unless the kubeconfig
// trusts it.
func httpsConnectProxy(t *testing.T, backend string) *httptest.Server {
	t.Helper()
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if backend == "" {
			http.Error(w, "cannot reach the cluster", http.StatusBadGateway)
			return
		}
		up, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", backend)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { _, _ = io.Copy(up, buf.Reader); _ = up.Close() }()
		go func() { _, _ = io.Copy(conn, up); _ = conn.Close() }()
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

// Test_probeCurrentKubeContext_Reachability checks that the current-context
// probe reports a cluster alive whenever its server answers at all, so a 401
// and a certificate the kubeconfig cannot verify both count as alive. Over
// https, only a connection that cannot be made counts as dead.
func Test_probeCurrentKubeContext_Reachability(t *testing.T) {
	t.Run("a 401 counts as reachable", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(srv.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeContextKubeconfig(t, destPath, "other", srv.URL, serverCAPEM(t, srv))
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("an unverifiable certificate counts as reachable", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		// Without a certificate-authority the probe checks the server against
		// the system roots, which do not trust httptest's certificate; the probe
		// must still count the server as reachable.
		writeContextKubeconfig(t, destPath, "other", srv.URL, nil)
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a cluster behind its proxy-url counts as reachable", func(t *testing.T) {
		// The cluster address is dead; only its proxy answers for it.
		closed := httptest.NewServer(http.NotFoundHandler())
		server := closed.URL
		closed.Close()
		proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(proxy.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{Server: server, ProxyURL: proxy.URL})
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a server routed by tls-server-name counts as reachable", func(t *testing.T) {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		// Like an SNI router, the server completes a handshake only for the name
		// it routes, so the probe must send tls-server-name to reach it.
		srv.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if hello.ServerName != "example.com" {
				return nil, errors.New("no route for this server name")
			}
			return nil, nil
		}}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{
			Server:                   srv.URL,
			CertificateAuthorityData: serverCAPEM(t, srv),
			TLSServerName:            "example.com",
		})
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a server that fails the request after the handshake counts as reachable", func(t *testing.T) {
		// A trusted cert completes the handshake, then the server drops the
		// connection before replying, as one demanding a client certificate
		// does after a TLS 1.3 handshake. The probe counts it reachable.
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		}))
		t.Cleanup(srv.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeContextKubeconfig(t, destPath, "other", srv.URL, serverCAPEM(t, srv))
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a closed port counts as unreachable", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // free the port so the probe's connection is refused
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeContextKubeconfig(t, destPath, "other", url, nil)
		assert.Assert(t, !probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a dead cluster with a certificate-authority path relative to the kubeconfig counts as unreachable", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		dir := t.TempDir()
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "ca.crt"), serverCAPEM(t, srv), 0o600))
		destPath := filepath.Join(dir, "config")
		t.Setenv("KUBECONFIG", destPath)
		// kubectl reads ca.crt from the kubeconfig's directory. A probe that
		// cannot read it leaves current-context alone, even for a dead cluster.
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{Server: url, CertificateAuthority: "ca.crt"})
		assert.Assert(t, !probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a kubeconfig with TLS settings that client-go refuses leaves current-context alone", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		// client-go, which kubectl uses, refuses a certificate-authority together
		// with insecure-skip-tls-verify. The probe then leaves current-context
		// alone, though the cluster is down.
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{
			Server:                   url,
			CertificateAuthorityData: serverCAPEM(t, srv),
			InsecureSkipTLSVerify:    true,
		})
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a dead cluster behind an https proxy with insecure-skip-tls-verify counts as unreachable", func(t *testing.T) {
		proxy := httpsConnectProxy(t, "")
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{
			Server:                "https://cluster.invalid:6443",
			ProxyURL:              proxy.URL,
			InsecureSkipTLSVerify: true,
		})
		assert.Assert(t, !probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("a live cluster behind an https proxy with insecure-skip-tls-verify counts as reachable", func(t *testing.T) {
		cluster := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(cluster.Close)
		proxy := httpsConnectProxy(t, cluster.Listener.Addr().String())
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		writeClusterKubeconfig(t, destPath, "other", &clientcmdapi.Cluster{
			Server:                cluster.URL,
			ProxyURL:              proxy.URL,
			InsecureSkipTLSVerify: true,
		})
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
	})

	t.Run("the probe does not send a client certificate", func(t *testing.T) {
		var served, sawCert atomic.Bool
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			served.Store(true)
			sawCert.Store(len(r.TLS.PeerCertificates) > 0)
		}))
		srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		destPath := filepath.Join(t.TempDir(), "config")
		t.Setenv("KUBECONFIG", destPath)
		certPEM := serverCAPEM(t, srv)
		cfg := clientcmdapi.NewConfig()
		cfg.Clusters["other"] = &clientcmdapi.Cluster{Server: srv.URL, CertificateAuthorityData: certPEM}
		cfg.AuthInfos["other"] = &clientcmdapi.AuthInfo{ClientCertificateData: certPEM, ClientKeyData: clientKeyPEM(t, srv)}
		cfg.Contexts["other"] = &clientcmdapi.Context{Cluster: "other", AuthInfo: "other"}
		assert.NilError(t, clientcmd.WriteToFile(*cfg, destPath))
		assert.Assert(t, probeCurrentKubeContext(t.Context(), "other"))
		assert.Assert(t, served.Load())
		assert.Assert(t, !sawCert.Load())
	})
}

// Test_Reconcile_AlreadyReady_LeavesCurrentContext checks that a reconcile
// which finds KubernetesReady already True does not probe or take over the
// user's current-context, even when that context's cluster is unreachable.
func Test_Reconcile_AlreadyReady_LeavesCurrentContext(t *testing.T) {
	dir := t.TempDir()
	srcPath := fakeK3sServer(t, filepath.Join(dir, "k3s.yaml"), http.StatusOK)
	isolateKubeconfig(t, dir)

	// current-context names a cluster that refuses connections; the old
	// periodic check would take it over.
	refusing := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refusingURL := refusing.URL
	refusing.Close()
	writeContextKubeconfig(t, filepath.Join(dir, ".kube", "config"), "other", refusingURL, nil)

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
		clientsetFn:            fakeClientset(readyNode()),
	}
	t.Cleanup(func() { r.removeKubeContext(context.Background()) })

	_, err := r.Reconcile(context.Background(), ctrl.Request{Name: appName})
	assert.NilError(t, err)
	// No probe goroutine should have started; wait in case one wrongly did.
	r.contextProbeWg.Wait()

	cfg := loadDest(t, filepath.Join(dir, ".kube", "config"))
	assert.Equal(t, cfg.CurrentContext, "other",
		"an already-Ready reconcile must not take over current-context")
	_, merged := cfg.Contexts[instance.Name()]
	assert.Assert(t, merged, "the instance context should still be merged in")
}

// Test_CheckCurrentKubeContext_LeavesUserChoiceAlone checks that when the user
// selects another context while the probe runs, the takeover does not overwrite
// that choice, because the switch compares current-context inside the write.
func Test_CheckCurrentKubeContext_LeavesUserChoiceAlone(t *testing.T) {
	dir := t.TempDir()
	isolateKubeconfig(t, dir)
	destPath := filepath.Join(dir, ".kube", "config")

	// current-context "other" points at a refusing server, so the probe reports
	// it unreachable and goes on to take it over.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	writeContextKubeconfig(t, destPath, "other", url, nil)

	// The user picks another context in the seam before the takeover writes.
	testBeforeSetCurrentKubeContext = func() {
		assert.NilError(t, setCurrentKubeContext("user-choice"))
	}
	t.Cleanup(func() { testBeforeSetCurrentKubeContext = nil })

	r := &KubernetesReconciler{
		K3sConfigPath:          makeSrcKubeconfig(t, dir),
		InstanceKubeConfigPath: filepath.Join(dir, "kube.config"),
	}
	assert.NilError(t, r.mergeKubeContext(context.Background()))
	r.checkCurrentKubeContext(context.Background())
	r.contextProbeWg.Wait()

	cfg := loadDest(t, destPath)
	assert.Equal(t, cfg.CurrentContext, "user-choice",
		"a context the user selected during the probe must stay current")
}
