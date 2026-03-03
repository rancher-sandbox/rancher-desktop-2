// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-logr/logr"

	"k8s.io/apiextensions-apiserver/pkg/apihelpers"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	// crdOwnerLabel is the label used to identify the SharedControllerManager that owns a CRD.
	crdOwnerLabel = "org.rancherdesktop.rdd/shared-controller-manager"
)

// SharedControllerManager manages all embedded RDD controllers using a single controller-runtime manager.
type SharedControllerManager struct {
	name            string
	manager         ctrl.Manager
	registrations   []base.Controller
	kubeConfig      *rest.Config
	metricsPort     int
	healthPort      int
	webhookPort     int
	passthroughPort int
	started         bool
	discovery       *ControllerManagerDiscoveryGroup
}

// NewSharedControllerManager creates a new shared controller manager.
func NewSharedControllerManager(ctx context.Context, name string, kubeConfig *rest.Config, metricsPort, healthPort int) (*SharedControllerManager, error) {
	// Create discovery service (errors handled in Start method)
	discovery, err := NewControllerManagerDiscoveryGroup(kubeConfig, name)
	if err != nil {
		return nil, err
	}

	// Calculate webhook port with instance offset
	desiredWebhookPort := 9443 + instance.Index()
	webhookPort, err := GetAvailablePort(ctx, desiredWebhookPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get available webhook port: %w", err)
	}

	desiredPassthroughPort := 9090 + instance.Index()
	passthroughPort, err := GetAvailablePort(ctx, desiredPassthroughPort)
	if err != nil {
		return nil, fmt.Errorf("failed to get available pass through port: %w", err)
	}

	return &SharedControllerManager{
		name:            name,
		kubeConfig:      kubeConfig,
		metricsPort:     metricsPort,
		healthPort:      healthPort,
		webhookPort:     webhookPort,
		passthroughPort: passthroughPort,
		registrations:   make([]base.Controller, 0),
		started:         false,
		discovery:       discovery,
	}, nil
}

// RegisterController registers a controller with the shared manager.
func (scm *SharedControllerManager) RegisterController(registration base.Controller) error {
	if scm.started {
		return fmt.Errorf("cannot register controller %s: shared manager already started", registration.GetName())
	}

	klog.V(2).InfoS("Registering controller with shared manager", "controller", registration.GetName(), "shared manager", scm.name)
	scm.registrations = append(scm.registrations, registration)
	return nil
}

// Start initializes the shared manager and starts all registered controllers.
func (scm *SharedControllerManager) Start(ctx context.Context) error {
	if scm.started {
		return fmt.Errorf("shared controller manager %s already started", scm.name)
	}

	log := klog.FromContext(ctx)

	// Clean up unused resources from previous controller runs
	if err := scm.cleanupUnusedResources(ctx); err != nil {
		return fmt.Errorf("failed to cleanup unused resources: %w", err)
	}

	// Clean up stale discovery configmap to prevent readiness check confusion
	if err := scm.discovery.UnregisterControllerManager(ctx); err != nil {
		log.Error(err, "Failed to cleanup stale discovery configmap, continuing with startup")
	}

	// Install CRDs for all registered controllers in parallel
	log.Info("Installing CRDs for all controllers in parallel", "controllers", len(scm.registrations))
	if err := scm.installControllerCRDs(ctx); err != nil {
		return fmt.Errorf("failed to install controller CRDs: %w", err)
	}

	// Configure controller-runtime to use klog
	ctrllog.SetLogger(log.WithName(fmt.Sprintf("controller-runtime-%s", scm.name)))

	// Create and register scheme with required types
	managerScheme := runtime.NewScheme()
	if err := scheme.AddToScheme(managerScheme); err != nil {
		return fmt.Errorf("failed to add core scheme: %w", err)
	}
	// Add CRD types to support dynamic resource discovery
	if err := apiextensionsv1.AddToScheme(managerScheme); err != nil {
		return fmt.Errorf("failed to add apiextensions scheme: %w", err)
	}

	// Modify kubeconfig to force JSON content type to avoid protobuf issues
	configCopy := *scm.kubeConfig
	configCopy.ContentType = "application/json"

	// Create the shared controller-runtime manager
	managerOptions := ctrl.Options{
		Scheme: managerScheme,
		Metrics: server.Options{
			BindAddress: "127.0.0.1:" + strconv.Itoa(scm.metricsPort),
		},
		HealthProbeBindAddress: "127.0.0.1:" + strconv.Itoa(scm.healthPort),
		LeaderElection:         false, // RDD controllers are single-instance
		// Limit graceful shutdown time to avoid blocking external controller exit.
		// Default is 30s which is too long when control plane disappears.
		GracefulShutdownTimeout: ptr.To(10 * time.Second),
	}

	// Only configure webhook server if controllers require it
	if scm.hasWebhookControllers() {
		// Use instance TLS directory for webhook certificates (persistent storage)
		webhookCertDir := instance.TLSDir()

		opts := webhook.Options{
			Host:     "127.0.0.1",
			Port:     scm.webhookPort,
			CertDir:  webhookCertDir,
			CertName: fmt.Sprintf("webhook-%s.crt", scm.name),
			KeyName:  fmt.Sprintf("webhook-%s.key", scm.name),
		}

		// Generate shared webhook certificates
		if err := scm.setupSharedWebhookCertificates(opts); err != nil {
			return fmt.Errorf("failed to setup shared webhook certificates: %w", err)
		}

		managerOptions.WebhookServer = webhook.NewServer(opts)
	}

	mgr, err := ctrl.NewManager(&configCopy, managerOptions)
	if err != nil {
		return fmt.Errorf("failed to create shared controller manager: %w", err)
	}

	scm.manager = mgr

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to set up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("failed to set up ready check: %w", err)
	}

	// Register controller manager in cluster for service discovery before
	// running the controllers.
	if err := scm.registerDiscovery(ctx); err != nil {
		log.Error(err, "Failed to register controller manager for discovery")
		// Don't fail startup for discovery registration errors
	}

	// Ensure cleanup on shutdown with a timeout to avoid blocking if apiserver is gone
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := scm.discovery.UnregisterControllerManager(cleanupCtx); err != nil {
			log.Error(err, "Failed to unregister controller manager")
		}
	}()

	// Add pass through server
	if err := mgr.Add(manager.RunnableFunc(scm.runPassthroughServer)); err != nil {
		return fmt.Errorf("failed to add pass through server to manager: %w", err)
	}

	// Track webhook setup goroutines
	var webhookWaitGroup sync.WaitGroup
	webhookErrors := make(chan error, len(scm.registrations))

	// Register all controllers with the manager and start webhook setup immediately
	for _, registration := range scm.registrations {
		klog.InfoS("Registering controller with shared manager", "controller", registration.GetName())

		// If the controller needs the webhook port, provide it
		if webhookController, ok := registration.(base.WebhookController); ok {
			klog.InfoS("Setting webhook port for controller", "controller", registration.GetName(), "port", scm.webhookPort)
			webhookController.SetWebhookPort(scm.webhookPort)
		}

		if err := registration.RegisterWithManager(mgr); err != nil {
			return fmt.Errorf("failed to register controller %s: %w", registration.GetName(), err)
		}

		// Start webhook setup immediately if this controller has webhooks
		if webhookController, ok := registration.(base.WebhookController); ok {
			managers := webhookController.GetWebhookManagers()
			for i, manager := range managers {
				if manager == nil {
					continue
				}
				name := registration.GetName()
				webhookWaitGroup.Add(1)
				go func() {
					defer webhookWaitGroup.Done()
					klog.Infof("Starting webhook setup for controller %s (webhook %d)", name, i)
					if err := manager.Setup(); err != nil {
						webhookErrors <- fmt.Errorf("controller %s webhook %d: %w", name, i, err)
					}
				}()
			}
		}
	}

	webhookWaitGroup.Wait()
	close(webhookErrors)

	var errs []error
	for err := range webhookErrors {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("webhook setup : %w", errors.Join(errs...))
	}

	klog.Info("All webhook configurations created successfully")

	scm.started = true

	klog.InfoS("Starting shared controller manager",
		"name", scm.name,
		"controllers", len(scm.registrations),
		"metricsPort", scm.metricsPort,
		"healthPort", scm.healthPort)

	// Start the manager (this blocks until context is cancelled)
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start shared controller manager: %w", err)
	}

	return nil
}

// GetManager returns the underlying controller-runtime manager
// This is useful for advanced use cases where direct access to the manager is needed.
func (scm *SharedControllerManager) GetManager() ctrl.Manager {
	return scm.manager
}

// IsStarted returns true if the shared manager has been started.
func (scm *SharedControllerManager) IsStarted() bool {
	return scm.started
}

// GetRegisteredControllers returns the names of all registered controllers.
func (scm *SharedControllerManager) GetRegisteredControllers() []string {
	names := make([]string, len(scm.registrations))
	for i, registration := range scm.registrations {
		names[i] = registration.GetName()
	}
	return names
}

// GetWebhookPort returns the webhook server port.
func (scm *SharedControllerManager) GetWebhookPort() int {
	return scm.webhookPort
}

// hasWebhookControllers checks if any registered controllers require webhook functionality.
func (scm *SharedControllerManager) hasWebhookControllers() bool {
	for _, registration := range scm.registrations {
		if _, ok := registration.(base.WebhookController); ok {
			return true
		}
	}
	return false
}

// registerDiscovery registers the controller manager information in the cluster.
func (scm *SharedControllerManager) registerDiscovery(ctx context.Context) error {
	// Get controller names, filtering out builtin controllers.
	// Builtin controllers are internal system controllers and should not be registered in discovery.
	var controllerNames []string
	passthroughEndpoints := make(map[string][]string)
	for _, registration := range scm.registrations {
		if registration.GetAPIGroup() != "builtin" {
			controllerNames = append(controllerNames, registration.GetName())
		}
		if httpController, ok := registration.(base.PassthroughController); ok {
			passthroughEndpoints[registration.GetName()] = httpController.GetPassthroughEndpoints()
		}
	}

	// Only register if there are non-builtin controllers.
	if len(controllerNames) == 0 {
		return nil
	}

	return scm.discovery.RegisterControllerManager(ctx, ControllerManagerInput{
		HealthPort:          scm.healthPort,
		MetricsPort:         scm.metricsPort,
		PassthroughPort:     scm.passthroughPort,
		EnabledControllers:  controllerNames,
		EnabledPassthroughs: passthroughEndpoints,
	})
}

// installControllerCRDs installs all controller CRDs in parallel for better performance.
func (scm *SharedControllerManager) installControllerCRDs(ctx context.Context) error {
	if len(scm.registrations) == 0 {
		return nil
	}

	apiextensionsClient, err := apiextensionsclientset.NewForConfig(scm.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	crdClient := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions()

	// Step 1: Create all CRDs that don't already exist
	type crdInfo struct {
		controller base.Controller
		crd        apiextensionsv1.CustomResourceDefinition
		needsWait  bool
	}

	var crdInfos []crdInfo

	for _, controller := range scm.registrations {
		controllerName := controller.GetName()
		crdData := controller.GetCRDData()

		// Skip controllers without CRDs (e.g., built-in controllers for Kubernetes resources)
		if crdData == "" {
			klog.V(2).InfoS("Controller has no CRD, skipping CRD installation", "controller", controllerName)
			continue
		}

		decoder := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(crdData))
		for {
			var crd apiextensionsv1.CustomResourceDefinition
			if err := decoder.Decode(&crd); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return fmt.Errorf("failed to decode CRD for controller %s: %w", controllerName, err)
			}
			if crd.Name == "" {
				continue // Comment block before the first document has no data.
			}

			// Check if CRD already exists
			_, err = crdClient.Get(ctx, crd.Name, metav1.GetOptions{})
			if err == nil {
				klog.Infof("%s CRD already exists", controllerName)
				crdInfos = append(crdInfos, crdInfo{controller: controller, crd: crd, needsWait: false})
				continue
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check if CRD exists for controller %s: %w", controllerName, err)
			}

			// Create the CRD
			klog.Infof("Installing %s CRD %s", controllerName, crd.Name)
			if crd.Labels == nil {
				crd.Labels = make(map[string]string)
			}
			crd.Labels[crdOwnerLabel] = scm.name
			if _, err := crdClient.Create(ctx, &crd, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("failed to create CRD for controller %s: %w", controllerName, err)
			}

			crdInfos = append(crdInfos, crdInfo{controller: controller, crd: crd, needsWait: true})
		}
	}

	// Step 2: Wait for all CRDs to be established
	// We need to wait for all CRDs before proceeding
	if len(crdInfos) == 0 {
		return nil
	}

	for _, info := range crdInfos {
		if !info.needsWait {
			continue
		}

		controllerName := info.controller.GetName()
		klog.Infof("Waiting for %s CRD to be established", controllerName)

		err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			crd, err := crdClient.Get(ctx, info.crd.Name, metav1.GetOptions{})
			if err != nil {
				klog.ErrorS(err, "Failed to get CRD during establishment wait", "controller", controllerName, "crd", info.crd.Name)
				return false, err
			}

			// Debug: Log current conditions
			klog.V(2).InfoS("Checking CRD conditions", "controller", controllerName, "conditions", len(crd.Status.Conditions))
			for i, condition := range crd.Status.Conditions {
				klog.V(3).InfoS("CRD condition", "controller", controllerName, "index", i, "type", condition.Type, "status", condition.Status, "reason", condition.Reason)
				if condition.Type == apiextensionsv1.Established && condition.Status == apiextensionsv1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		})
		if err != nil {
			return fmt.Errorf("failed to establish CRD for controller %s: %w", controllerName, err)
		}
		klog.Infof("%s CRD is established", controllerName)
	}

	klog.Infof("All %d CRDs are established", len(crdInfos))
	return nil
}

func (scm *SharedControllerManager) runPassthroughServer(ctx context.Context) error {
	hasPassthroughServers := false
	log := klog.FromContext(ctx)

	mux := http.NewServeMux()
	for _, registration := range scm.registrations {
		if httpController, ok := registration.(base.PassthroughController); ok {
			hasPassthroughServers = true
			for _, endpoint := range httpController.GetPassthroughEndpoints() {
				handler := httpController.GetPassthroughHandler(endpoint)
				prefix := fmt.Sprintf("/%s/%s", registration.GetName(), endpoint)
				log.V(2).Info("Registering passthrough endpoint",
					"controller", registration.GetName(), "endpoint", prefix)
				mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))
			}
		}
	}

	if !hasPassthroughServers {
		klog.V(2).InfoS("No pass through controllers registered, skipping pass through server startup")
		return nil
	}

	server := http.Server{
		Addr:     fmt.Sprintf("localhost:%d", scm.passthroughPort),
		Handler:  mux,
		ErrorLog: slog.NewLogLogger(logr.ToSlogHandler(klog.FromContext(ctx)), slog.LevelError),
	}

	shutdownComplete := make(chan struct{})
	go func() {
		defer close(shutdownComplete)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Error(err, "failed to shutdown pass through server")
		}
	}()

	log.V(2).Info("Starting pass through server", "addr", server.Addr)
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("pass through server failed: %w", err)
	}
	<-shutdownComplete

	return nil
}

// cleanupUnusedResources removes CRDs and webhook configurations for controllers that are not currently running.
// This function cleans up resources from controllers that were previously running but are no longer selected.
func (scm *SharedControllerManager) cleanupUnusedResources(ctx context.Context) error {
	// Get the set of controllers that will be running
	runningControllers := make(map[string]bool)
	for _, registration := range scm.registrations {
		runningControllers[registration.GetName()] = true
	}

	// Get all possible controllers from the registry to determine what could be cleaned up
	allControllers := base.GetAllControllers()
	controllersToCleanup := make([]base.Controller, 0)

	for _, controller := range allControllers {
		if !runningControllers[controller.GetName()] {
			controllersToCleanup = append(controllersToCleanup, controller)
		}
	}

	if len(controllersToCleanup) == 0 {
		klog.V(2).InfoS("No controllers to cleanup")
		return nil
	}

	klog.InfoS("Cleaning up resources for unused controllers", "count", len(controllersToCleanup))

	// Cleanup CRDs
	if err := scm.cleanupUnusedCRDs(ctx, controllersToCleanup); err != nil {
		return fmt.Errorf("failed to cleanup unused CRDs: %w", err)
	}

	return nil
}

// cleanupUnusedCRDs removes CRDs for controllers that are not currently running
// and waits for the deletions to complete.
func (scm *SharedControllerManager) cleanupUnusedCRDs(ctx context.Context, controllersToCleanup []base.Controller) error {
	if len(controllersToCleanup) == 0 {
		return nil
	}

	apiextensionsClient, err := apiextensionsclientset.NewForConfig(scm.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create apiextensions client: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(scm.kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	crdClient := apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions()

	var deletedCRDs []*apiextensionsv1.CustomResourceDefinition

	for _, controller := range controllersToCleanup {
		crdData := controller.GetCRDData()
		if crdData == "" {
			continue
		}

		decoder := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(crdData))
		for {
			var crd apiextensionsv1.CustomResourceDefinition
			if err := decoder.Decode(&crd); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				klog.ErrorS(err, "Failed to unmarshal CRD for controller", "controller", controller.GetName())
				continue
			}
			if crd.Name == "" {
				continue // Comment block before the first document has no data.
			}

			existingCRD, err := crdClient.Get(ctx, crd.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to check CRD %s: %w", crd.Name, err)
			}
			if existingCRD.Labels != nil && existingCRD.Labels[crdOwnerLabel] != scm.name {
				klog.V(2).InfoS("Skipping CRD owned by different controller manager", "crd", crd.Name, "manager", existingCRD.Labels[crdOwnerLabel])
				continue
			}

			klog.InfoS("Deleting unused CRD", "crd", crd.Name, "controller", controller.GetName())
			if err := crdClient.Delete(ctx, crd.Name, metav1.DeleteOptions{}); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return fmt.Errorf("failed to delete CRD %s: %w", crd.Name, err)
			}
			deletedCRDs = append(deletedCRDs, existingCRD)
		}
	}

	if len(deletedCRDs) == 0 {
		return nil
	}

	// Wait for all deleted CRDs to be fully removed. On each iteration,
	// re-warm the watch cache for CRDs that still exist. CRD deletions and
	// finalizations trigger API group handler rebuilds that tear down watch
	// caches for sibling CRDs. Without re-warming, the CRD finalizer hits
	// 429s ("storage is (re)initializing") and enters exponential backoff
	// that can exceed the cleanup timeout.
	klog.InfoS("Waiting for CRD deletions to complete", "count", len(deletedCRDs))
	return wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		allGone := true
		for _, crd := range deletedCRDs {
			_, err := crdClient.Get(ctx, crd.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("failed to check CRD %s: %w", crd.Name, err)
			}
			warmCRDCache(ctx, dynamicClient, crd)
			allGone = false
		}
		return allGone, nil
	})
}

// warmCRDCache issues a List request for a custom resource type to initialize
// the API server's watch cache before the CRD finalizer needs it.
//
// On a fresh restart, the API server creates watch caches on demand. The CRD
// finalizer lists instances during deletion, which triggers lazy cache creation
// and receives HTTP 429 ("storage is (re)initializing") because
// ResilientWatchCacheInitialization (locked on since K8s v1.34) rejects
// immediately instead of blocking. The finalizer enters exponential backoff,
// which can exceed our cleanup timeout.
//
// Caches are not stable: any CRD deletion or finalization triggers an API group
// handler rebuild that tears down all watch caches in the group. Callers must
// re-warm repeatedly, not just once.
func warmCRDCache(ctx context.Context, dynamicClient dynamic.Interface, crd *apiextensionsv1.CustomResourceDefinition) {
	version, err := apihelpers.GetCRDStorageVersion(crd)
	if err != nil {
		return
	}

	gvr := schema.GroupVersionResource{
		Group:    crd.Spec.Group,
		Version:  version,
		Resource: crd.Spec.Names.Plural,
	}

	// The cache typically initializes in under 25ms, so a few short retries suffice.
	// Only retry on 429 (storage initializing); any other result means the cache is ready
	// or the error is unrelated and not worth blocking on.
	err = wait.PollUntilContextTimeout(ctx, 10*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		_, listErr := dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
		return !apierrors.IsTooManyRequests(listErr), nil
	})
	if err != nil {
		klog.V(2).InfoS("Cache warm-up timed out; proceeding with CRD deletion", "crd", crd.Name, "gvr", gvr)
	}
}

// setupSharedWebhookCertificates generates webhook certificates for all controllers that need them.
func (scm *SharedControllerManager) setupSharedWebhookCertificates(opts webhook.Options) error {
	// Check if any controller requires webhook certificates
	needsCertificates := false
	serviceNames := []string{}
	for _, registration := range scm.registrations {
		if webhookController, ok := registration.(base.WebhookController); ok {
			needsCertificates = true
			// Collect service names from webhook controllers
			if serviceName := webhookController.GetWebhookServiceName(); serviceName != "" {
				serviceNames = append(serviceNames, serviceName)
			}
		}
	}

	if !needsCertificates {
		klog.V(2).InfoS("No webhook controllers registered, skipping certificate setup")
		return nil
	}

	klog.InfoS("Setting up shared webhook certificates", "certDir", opts.CertDir, "serviceNames", serviceNames)

	// Use the shared webhook certificate manager to generate certificates
	certManager := base.NewSharedWebhookCertificateManager(
		opts.CertDir,
		opts.CertName,
		opts.KeyName,
		"127.0.0.1",
		serviceNames,
	)

	// Skip certificate generation if valid certificates already exist
	if certManager.CertificatesExist() {
		klog.InfoS("Valid webhook certificates already exist, skipping generation")
		return nil
	}

	if err := certManager.GenerateWebhookCertificates(); err != nil {
		return fmt.Errorf("failed to generate webhook certificates: %w", err)
	}

	klog.InfoS("Successfully generated shared webhook certificates")
	return nil
}
