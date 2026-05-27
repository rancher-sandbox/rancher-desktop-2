// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the Kubernetes context reconciler.
package controllers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// contextMu protects contextProbeCancel and contextProbeGen.
	contextMu sync.Mutex
	// contextProbeCancel cancels the in-flight current-context probe goroutine.
	contextProbeCancel context.CancelFunc
	// contextProbeGen detects superseded goroutines.
	contextProbeGen int
	// contextProbeWg lets removeKubeContext wait for any probe to finish.
	contextProbeWg sync.WaitGroup
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
		r.removeKubeContext(ctx)
		return ctrl.Result{}, r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonNotApplicable,
			"Kubernetes is not enabled",
		)
	}

	// Kubernetes is enabled but the VM is not (yet) running.
	if !running {
		r.removeKubeContext(ctx)
		return ctrl.Result{}, r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonNotRunning,
			"VM is not running",
		)
	}

	// Probe the k3s API server from the instance kubeconfig.
	healthy, err := r.probeK3sAPI(ctx)
	if err != nil {
		// kubeconfig missing or unreadable — k3s has not started yet.
		log.V(1).Info("Cannot probe k3s API server", "err", err)
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonProbing,
			"Waiting for k3s API server",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
	}
	if !healthy {
		log.V(1).Info("k3s API server not yet healthy")
		if condErr := r.setKubeCondition(ctx, &app,
			metav1.ConditionFalse,
			appv1alpha1.AppKubernetesReasonProbing,
			"Waiting for k3s API server",
		); condErr != nil {
			return ctrl.Result{}, condErr
		}
		return ctrl.Result{RequeueAfter: kubeProbeRequeue}, nil
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

	return ctrl.Result{}, r.setKubeCondition(ctx, &app,
		metav1.ConditionTrue,
		appv1alpha1.AppKubernetesReasonReady,
		"Kubernetes API server is ready",
	)
}

// probeK3sAPI reads the instance kubeconfig from r.K3sConfigPath and GETs
// /healthz on the k3s API server. Returns (false, err) when the kubeconfig
// is unreadable, (false, nil) when the server is unhealthy, and (true, nil)
// on success.
func (r *KubernetesReconciler) probeK3sAPI(ctx context.Context) (bool, error) {
	kubeconfigPath := r.K3sConfigPath
	data, err := os.ReadFile(kubeconfigPath)
	if err != nil {
		return false, fmt.Errorf("read instance kubeconfig: %w", err)
	}

	cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return false, fmt.Errorf("parse instance kubeconfig: %w", err)
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
			return false, fmt.Errorf("load client cert: %w", err)
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
		return false, fmt.Errorf("build healthz request: %w", err)
	}
	if cfg.BearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.BearerToken)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		logf.FromContext(ctx).V(1).Info("k3s healthz request failed", "url", cfg.Host+"/healthz", "err", err)
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logf.FromContext(ctx).V(1).Info("k3s healthz returned non-200", "url", cfg.Host+"/healthz", "status", resp.StatusCode)
	}
	return resp.StatusCode == http.StatusOK, nil
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

	r.contextMu.Lock()
	if r.contextProbeCancel != nil {
		r.contextProbeCancel()
	}
	probeCtx, cancel := context.WithCancel(ctx)
	r.contextProbeCancel = cancel
	r.contextProbeGen++
	myGen := r.contextProbeGen
	r.contextMu.Unlock()

	r.contextProbeWg.Add(1)
	go func() {
		// Use sync.Once so contextProbeWg.Done() fires exactly once, either
		// explicitly after the HTTP probe or via defer on early return.
		// Crucially, Done() must be called BEFORE setCurrentKubeContext so that
		// removeKubeContext.Wait() is not held hostage by a slow or blocking
		// write to ~/.kube/config.
		var wgDone sync.Once
		signalDone := func() { wgDone.Do(r.contextProbeWg.Done) }
		defer signalDone()
		defer func() {
			r.contextMu.Lock()
			if r.contextProbeGen == myGen {
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

		// Signal the WaitGroup before writing to ~/.kube/config so that
		// removeKubeContext.Wait() is not blocked by a slow file write.
		signalDone()

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
