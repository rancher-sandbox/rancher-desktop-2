// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Kubernetes context reconciler.
package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/predicates"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	appName = "app"

	// kubeProbeTimeout is the per-probe deadline for reaching the k3s API server.
	kubeProbeTimeout = 3 * time.Second

	// kubeProbeRequeue is the wait between probes when k3s is not yet ready.
	kubeProbeRequeue = 5 * time.Second

	// kubeHealthyRequeue is the wait between probes when k3s is healthy. Longer
	// than kubeProbeRequeue to keep steady-state log noise down; sets the
	// worst-case latency for noticing an unannounced k3s death.
	kubeHealthyRequeue = 15 * time.Second

	// probeFailureThreshold is the number of consecutive ambiguous probe
	// failures tolerated while Ready before flipping to Probing. Decisive
	// failures (Unreachable, Unhealthy) bypass the threshold and flip
	// immediately. Ambiguous = the probe could not complete within
	// kubeProbeTimeout, which on a busy laptop is often transient load
	// rather than k3s genuinely going down.
	probeFailureThreshold = 3
)

// probeResult classifies a probe outcome so Reconcile can apply different
// policies. The split exists because not every failure means the same thing:
// ECONNREFUSED is k3s being gone, an HTTP 5xx is k3s saying "I'm not ready"
// on purpose, and a timeout is the only genuinely ambiguous case that
// warrants smoothing.
type probeResult int

const (
	probeHealthy     probeResult = iota // /healthz returned 200
	probeUnreachable                    // TCP listener gone or connection torn down mid-request
	probeUnhealthy                      // /healthz returned non-200
	probeAmbiguous                      // request did not complete within kubeProbeTimeout
)

func (r probeResult) String() string {
	switch r {
	case probeHealthy:
		return "Healthy"
	case probeUnreachable:
		return "Unreachable"
	case probeUnhealthy:
		return "Unhealthy"
	case probeAmbiguous:
		return "Ambiguous"
	default:
		return fmt.Sprintf("probeResult(%d)", int(r))
	}
}

// KubernetesReconciler watches the App resource and manages the
// rancher-desktop-{instance} context in ~/.kube/config.
type KubernetesReconciler struct {
	client.Client

	// K3sConfigPath is the path to the in-VM k3s kubeconfig mirrored by the
	// Lima probe. Production wiring sets it from instance.K3sConfig(); tests
	// inject a path under a temp directory.
	K3sConfigPath string

	// InstanceKubeConfigPath is where the standalone instance kubeconfig (only
	// the rancher-desktop-{instance} context) is published for rdd run to
	// consume. Production wiring sets it from instance.KubeConfig(); tests
	// inject a path under a temp directory.
	InstanceKubeConfigPath string

	// contextMu protects contextProbeCancel.
	contextMu sync.Mutex
	// contextProbeCancel cancels the in-flight current-context probe goroutine.
	contextProbeCancel context.CancelFunc
	// contextProbeWg lets removeKubeContext wait for any probe to finish.
	contextProbeWg sync.WaitGroup

	// consecutiveProbeFailures counts ambiguous probe failures while the
	// KubernetesReady condition was True. Resets on any success or
	// decisive failure (Unreachable, Unhealthy). Crossing
	// probeFailureThreshold flips Ready to Probing and resets the counter.
	// Reconcile runs single-threaded (MaxConcurrentReconciles=1), so the
	// counter needs no synchronization.
	consecutiveProbeFailures int

	// probeFn lets tests inject probe results without making real HTTP calls.
	// Production wiring leaves it nil; Reconcile then calls probeK3sAPI.
	probeFn func(ctx context.Context) (probeResult, error)

	// clientsetFn lets tests inject a fake typed client so the node check
	// runs against in-memory objects. Production wiring leaves it nil;
	// checkNodeReady then builds a client from the instance kubeconfig.
	clientsetFn func() (kubernetes.Interface, error)
}

// probe dispatches to probeFn if injected (tests) or probeK3sAPI otherwise.
func (r *KubernetesReconciler) probe(ctx context.Context) (probeResult, error) {
	if r.probeFn != nil {
		return r.probeFn(ctx)
	}
	return r.probeK3sAPI(ctx)
}

// checkNodeReady reports whether any node has reached Ready, so a scheduled
// workload has somewhere to run. It returns (ready, reason, message); reason
// and message describe the blocking gate only when ready is false.
// Workload-level readiness (coredns, traefik) is deliberately NOT gated here:
// it would delay Ready for every user, and consumers that need it can wait
// for those Deployments themselves.
func (r *KubernetesReconciler) checkNodeReady(ctx context.Context) (ready bool, reason, message string) {
	cs, err := r.getClientset()
	if err != nil {
		return false, appv1alpha1.AppKubernetesReasonWaitingForNode,
			fmt.Sprintf("Cannot reach cluster: %v", err)
	}

	checkCtx, cancel := context.WithTimeout(ctx, kubeProbeTimeout)
	defer cancel()

	nodes, err := cs.CoreV1().Nodes().List(checkCtx, metav1.ListOptions{})
	if err != nil {
		return false, appv1alpha1.AppKubernetesReasonWaitingForNode,
			fmt.Sprintf("Cannot list nodes: %v", err)
	}
	if len(nodes.Items) == 0 {
		return false, appv1alpha1.AppKubernetesReasonWaitingForNode, "No node has registered yet"
	}
	for i := range nodes.Items {
		if nodeReady(&nodes.Items[i]) {
			return true, "", ""
		}
	}

	return false, appv1alpha1.AppKubernetesReasonWaitingForNode, "No node is Ready yet"
}

// getClientset returns a typed client for the k3s cluster inside the VM.
// r.Client is no substitute: it talks to rdd's embedded control plane,
// which hosts the App resource but no Nodes. Tests inject a fake via
// clientsetFn.
func (r *KubernetesReconciler) getClientset() (kubernetes.Interface, error) {
	if r.clientsetFn != nil {
		return r.clientsetFn()
	}
	data, err := os.ReadFile(r.K3sConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read instance kubeconfig: %w", err)
	}
	cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse instance kubeconfig: %w", err)
	}
	cfg.Timeout = kubeProbeTimeout
	return kubernetes.NewForConfig(cfg)
}

// nodeReady reports whether node carries a Ready condition set to True.
func nodeReady(node *corev1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Reconcile drives the KubernetesReady condition and ~/.kube/config lifecycle.
func (r *KubernetesReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appv1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	running := apimeta.IsStatusConditionTrue(app.Status.Conditions, appv1alpha1.AppConditionRunning)

	log.V(1).Info("reconcile entered",
		"kubernetesEnabled", app.Spec.Kubernetes.Enabled,
		"running", running,
		"generation", app.Generation,
		"resourceVersion", app.ResourceVersion,
	)

	// When Kubernetes is disabled, stamp the condition and clean up.
	if !app.Spec.Kubernetes.Enabled {
		r.resetProbeFailures()
		r.removeKubeContext(ctx)
		return ctrl.Result{}, r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonNotApplicable,
			"Kubernetes is not enabled",
		)
	}

	// Kubernetes is enabled but the VM is not (yet) running.
	if !running {
		r.resetProbeFailures()
		r.removeKubeContext(ctx)
		return ctrl.Result{}, r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonNotRunning,
			"VM is not running",
		)
	}

	// Probe the k3s API server from the instance kubeconfig.
	result, err := r.probe(ctx)
	if err != nil {
		// kubeconfig missing or unreadable — k3s has not started yet.
		log.V(1).Info("Cannot probe k3s API server", "err", err)
		r.resetProbeFailures()
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonProbing,
			"Waiting for k3s API server",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
	}

	switch result {
	case probeUnreachable, probeUnhealthy:
		// Decisive failure: k3s is gone or said no. Flip immediately
		// without smoothing so the user sees the state change promptly.
		log.V(1).Info("k3s API server not yet healthy", "result", result)
		r.resetProbeFailures()
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonProbing,
			"Waiting for k3s API server",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil

	case probeAmbiguous:
		// Probe did not complete within kubeProbeTimeout. While Ready, treat
		// the first probeFailureThreshold-1 ambiguous failures as transient
		// (machine under heavy load, k3s temporarily slow) and only flip to
		// Probing once the streak passes the threshold. During startup, the
		// condition is not yet Ready, so report Probing immediately.
		failures := r.incrementProbeFailures()
		currentlyReady := apimeta.IsStatusConditionTrue(
			app.Status.Conditions, appv1alpha1.AppConditionKubernetesReady)
		if currentlyReady && failures < probeFailureThreshold {
			log.V(1).Info("ambiguous probe failure tolerated",
				"failures", failures, "threshold", probeFailureThreshold)
			return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
		}
		if currentlyReady {
			log.V(1).Info("ambiguous probe failures crossed threshold",
				"failures", failures, "threshold", probeFailureThreshold)
		} else {
			log.V(1).Info("ambiguous probe failure while not Ready",
				"failures", failures)
		}
		r.resetProbeFailures()
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonProbing,
			"Waiting for k3s API server",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil

	case probeHealthy:
		r.resetProbeFailures()
		// Fall through to manageKubeContext / setReady below.
	}

	// k3s is healthy — merge the context into ~/.kube/config.
	if err := r.manageKubeContext(ctx); err != nil {
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonMergeFailed,
			fmt.Sprintf("Failed to publish Kubernetes context: %v", err),
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
	}

	// /healthz answers before the node registers, so reporting Ready here
	// would let `rdd set` return while `kubectl run` still sits Pending.
	// Gate the first Ready on node Ready. Once Ready, ignore the status of
	// the node, so a transient node blip does not flap Settled.
	if !apimeta.IsStatusConditionTrue(app.Status.Conditions, appv1alpha1.AppConditionKubernetesReady) {
		ready, reason, message := r.checkNodeReady(ctx)
		if !ready {
			log.V(1).Info("k3s API healthy but cluster not yet usable",
				"reason", reason, "message", message)
			if condErr := r.setKubeCondition(ctx, &app, metav1.ConditionFalse, reason, message); condErr != nil {
				return ctrl.Result{}, condErr
			}
			return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
		}
	}

	// Requeue periodically so a later k3s death surfaces even if no other
	// controller writes to App in the meantime. Without this, setKubeCondition
	// is a no-op when Ready→Ready and there is nothing else to trigger a
	// re-probe until the controller-runtime default resync (~10 min).
	return ctrl.Result{RequeueAfter: kubeHealthyRequeue}, r.setKubeCondition(ctx, &app,
		metav1.ConditionTrue,
		appv1alpha1.AppKubernetesReasonReady,
		"Kubernetes cluster is ready",
	)
}

// resetProbeFailures clears the ambiguous-failure streak counter.
func (r *KubernetesReconciler) resetProbeFailures() {
	r.consecutiveProbeFailures = 0
}

// incrementProbeFailures bumps the ambiguous-failure streak counter and
// returns the new value.
func (r *KubernetesReconciler) incrementProbeFailures() int {
	r.consecutiveProbeFailures++
	return r.consecutiveProbeFailures
}

// probeK3sAPI reads the instance kubeconfig from r.K3sConfigPath and GETs
// /healthz on the k3s API server. Returns an error only when the kubeconfig
// cannot be loaded; otherwise the result classifies the outcome. See the
// probeResult type for the meaning of each value.
func (r *KubernetesReconciler) probeK3sAPI(ctx context.Context) (probeResult, error) {
	kubeconfigPath := r.K3sConfigPath
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return probeUnreachable, fmt.Errorf("read instance kubeconfig: %w", err)
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return probeUnreachable, fmt.Errorf("parse instance kubeconfig: %w", err)
	}

	// Build a TLS-aware HTTP client from the REST config.
	tlsCfg := &tls.Config{ServerName: cfg.ServerName}
	if len(cfg.CAData) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(cfg.CAData)
		tlsCfg.RootCAs = pool
	}
	// Load client cert for mTLS auth (k3s kubeconfig uses client cert, not bearer token).
	if len(cfg.CertData) > 0 && len(cfg.KeyData) > 0 {
		cert, err := tls.X509KeyPair(cfg.CertData, cfg.KeyData)
		if err != nil {
			return probeUnreachable, fmt.Errorf("load client cert: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}

	probeCtx, cancel := context.WithTimeout(ctx, kubeProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, cfg.Host+"/healthz", http.NoBody)
	if err != nil {
		return probeUnreachable, fmt.Errorf("build healthz request: %w", err)
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		result := classifyProbeError(err)
		logf.FromContext(ctx).V(1).Info("k3s healthz request failed",
			"url", cfg.Host+"/healthz", "err", err, "result", result)
		return result, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logf.FromContext(ctx).V(1).Info("k3s healthz returned non-200",
			"url", cfg.Host+"/healthz", "status", resp.StatusCode)
		return probeUnhealthy, nil
	}
	return probeHealthy, nil
}

// classifyProbeError maps a transport-level failure from the healthz probe
// to a probeResult. Only timeouts count as ambiguous; everything else is
// treated as decisively unreachable so the reconciler reports the state
// honestly without smoothing. That bucket covers ECONNREFUSED (k3s listener
// gone), EOF or ECONNRESET (connection closed mid-request), DNS failures,
// and any unrecognized transport error.
func classifyProbeError(err error) probeResult {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT) {
		return probeAmbiguous
	}
	return probeUnreachable
}

// manageKubeContext merges the instance kubeconfig into ~/.kube/config and
// launches a goroutine to set current-context if the existing one is unhealthy.
// Returns an error if the synchronous merge fails; failures from the async
// current-context probe are logged but not returned.
// At most one probe runs at a time.
func (r *KubernetesReconciler) manageKubeContext(ctx context.Context) error {
	contextName := instance.Name()
	log := logf.FromContext(ctx).WithName("kube-context")

	if err := createReplaceKubeContext(contextName, r.K3sConfigPath); err != nil {
		log.Error(err, "Failed to create Kubernetes context", "context", contextName)
		return fmt.Errorf("create Kubernetes context %q: %w", contextName, err)
	}

	if err := writeInstanceKubeConfig(contextName, r.K3sConfigPath, r.InstanceKubeConfigPath); err != nil {
		log.Error(err, "Failed to write instance kubeconfig", "context", contextName)
		return fmt.Errorf("write instance kubeconfig for %q: %w", contextName, err)
	}

	r.contextMu.Lock()
	if r.contextProbeCancel != nil {
		r.contextProbeCancel()
	}
	probeCtx, cancel := context.WithCancel(ctx)
	r.contextProbeCancel = cancel
	r.contextMu.Unlock()

	r.contextProbeWg.Add(1)
	go func() {
		// Done fires after the kubeconfig write below, so removeKubeContext.Wait()
		// covers that write instead of racing it.
		defer r.contextProbeWg.Done()
		defer func() {
			// Both paths that replace contextProbeCancel cancel probeCtx first, so
			// an uncancelled probeCtx means the field still holds our cancel func.
			r.contextMu.Lock()
			if probeCtx.Err() == nil {
				r.contextProbeCancel = nil
			}
			r.contextMu.Unlock()
			cancel()
		}()

		current, err := getCurrentKubeContext()
		if err != nil {
			log.Error(err, "Failed to read current Kubernetes context")
			return
		}

		healthy := probeCurrentKubeContext(probeCtx, current)

		// Guard against writing after removeKubeContext cancelled probeCtx.
		if !healthy && probeCtx.Err() == nil {
			if err := setCurrentKubeContext(contextName); err != nil {
				log.Error(err, "Failed to set current Kubernetes context", "context", contextName)
			}
		}
	}()
	return nil
}

// probeCurrentKubeContext returns true if the named context's API server is
// healthy. Empty or "default" contexts are treated as unhealthy.
//
// Returns true on unexpected internal errors (path lookup, kubeconfig parse,
// TLS setup) to leave the user's current-context alone; each such path logs
// the underlying error.
func probeCurrentKubeContext(ctx context.Context, current string) bool {
	log := logf.FromContext(ctx).WithName("kube-context-probe").WithValues("context", current)
	if current == "" || current == "default" {
		return false
	}
	destPath, err := kubeConfigPath()
	if err != nil {
		log.Error(err, "Failed to resolve kubeconfig path")
		return true
	}
	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		log.Error(err, "Failed to load kubeconfig", "path", destPath)
		return true
	}
	kctx, ok := cfg.Contexts[current]
	if !ok {
		return false
	}
	clusterEntry, ok := cfg.Clusters[kctx.Cluster]
	if !ok {
		return false
	}

	// Build a REST config from the extracted cluster+user for the TLS probe.
	merged := *cfg
	merged.CurrentContext = current
	data, err := clientcmd.Write(merged)
	if err != nil {
		log.Error(err, "Failed to serialize kubeconfig")
		return true
	}
	restCfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		log.Error(err, "Failed to build REST config from kubeconfig")
		return true
	}

	tlsCfg := &tls.Config{
		ServerName: restCfg.ServerName,
	}
	if len(restCfg.CAData) > 0 {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(restCfg.CAData)
		tlsCfg.RootCAs = pool
	}
	// Load client cert for mTLS auth (k3s kubeconfig uses client cert, not bearer token).
	if len(restCfg.CertData) > 0 && len(restCfg.KeyData) > 0 {
		cert, err := tls.X509KeyPair(restCfg.CertData, restCfg.KeyData)
		if err != nil {
			log.Error(err, "Failed to load client cert")
			return true
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	httpClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
	probeCtx, cancel := context.WithTimeout(ctx, kubeProbeTimeout)
	defer cancel()

	url := clusterEntry.Server + "/healthz"
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		log.Error(err, "Failed to build healthz request", "url", url)
		return true
	}
	if restCfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+restCfg.BearerToken)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// removeKubeContext cancels any in-flight probe, waits for it to finish, then
// removes the context entries and clears current-context if it matched.
func (r *KubernetesReconciler) removeKubeContext(ctx context.Context) {
	r.contextMu.Lock()
	if r.contextProbeCancel != nil {
		r.contextProbeCancel()
		r.contextProbeCancel = nil
	}
	r.contextMu.Unlock()

	r.contextProbeWg.Wait()

	contextName := instance.Name()
	log := logf.FromContext(ctx).WithName("kube-context")

	if err := clearCurrentKubeContext(contextName); err != nil {
		log.Error(err, "Failed to clear current Kubernetes context", "context", contextName)
	}
	if err := deleteKubeContext(contextName); err != nil {
		log.Error(err, "Failed to delete Kubernetes context", "context", contextName)
	}
	if err := removeInstanceKubeConfig(r.InstanceKubeConfigPath); err != nil {
		log.Error(err, "Failed to remove instance kubeconfig", "context", contextName)
	}
}

// setKubeCondition updates KubernetesReady on the App resource.
// RetryOnConflict handles concurrent writes from other controllers.
func (r *KubernetesReconciler) setKubeCondition(ctx context.Context, app *appv1alpha1.App, status metav1.ConditionStatus, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appv1alpha1.App{}
		if err := r.Get(ctx, client.ObjectKey{Name: app.Name}, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               appv1alpha1.AppConditionKubernetesReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: latest.Generation,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager registers the reconciler with the manager.
func (r *KubernetesReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1alpha1.App{}, builder.WithPredicates(predicates.WatchEventLogger("kubernetes-reconciler"))).
		Named("kubernetes-reconciler").
		Complete(r)
}
