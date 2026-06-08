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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
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
// The metricsPort and healthPort are desired ports; Start resolves them
// to available ports immediately before binding.
func NewSharedControllerManager(name string, kubeConfig *rest.Config, metricsPort, healthPort int) (*SharedControllerManager, error) {
	discovery, err := NewControllerManagerDiscoveryGroup(kubeConfig, name)
	if err != nil {
		return nil, err
	}

	return &SharedControllerManager{
		name:            name,
		kubeConfig:      kubeConfig,
		metricsPort:     metricsPort,
		healthPort:      healthPort,
		webhookPort:     9443 + instance.Index(),
		passthroughPort: 9090 + instance.Index(),
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

	// Resolve all ports immediately before use to minimize the window
	// between releasing the test listener and controller-runtime rebinding.
	var err error
	scm.metricsPort, err = ResolvePort(ctx, scm.metricsPort)
	if err != nil {
		return fmt.Errorf("failed to resolve metrics port: %w", err)
	}
	scm.healthPort, err = ResolvePort(ctx, scm.healthPort)
	if err != nil {
		return fmt.Errorf("failed to resolve health port: %w", err)
	}
	scm.webhookPort, err = ResolvePort(ctx, scm.webhookPort)
	if err != nil {
		return fmt.Errorf("failed to resolve webhook port: %w", err)
	}
	scm.passthroughPort, err = ResolvePort(ctx, scm.passthroughPort)
	if err != nil {
		return fmt.Errorf("failed to resolve passthrough port: %w", err)
	}

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

	// Mark the control plane ready after registerDiscovery so clients
	// waiting on the ready annotation see both CRDs installed and
	// controller registration written, not just CRDs.
	if err := MarkControlPlaneReady(ctx, scm.discovery.client); err != nil {
		return fmt.Errorf("failed to mark control plane as ready: %w", err)
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

	// Register all controllers before launching webhook goroutines.
	// RegisterWithManager calls AddToScheme, which writes to the scheme map.
	// Webhook setup reads the scheme via client.Apply. Running both concurrently
	// causes a fatal "concurrent map read and map write" crash.
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
	}

	// Now that all scheme mutations are done, set up webhooks in parallel.
	var webhookWaitGroup sync.WaitGroup
	webhookErrors := make(chan error, len(scm.registrations))

	for _, registration := range scm.registrations {
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

	// Step 1: Create or update all CRDs
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

			// Update existing CRDs so new schema fields take effect on upgrade.
			existing, err := crdClient.Get(ctx, crd.Name, metav1.GetOptions{})
			if err == nil {
				if !apiequality.Semantic.DeepEqual(existing.Spec, crd.Spec) {
					existing.Spec = crd.Spec
					if _, err := crdClient.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
						return fmt.Errorf("failed to update CRD for controller %s: %w", controllerName, err)
					}
					klog.Infof("Updated %s CRD %s", controllerName, crd.Name)
				} else {
					klog.V(2).Infof("%s CRD %s is up to date", controllerName, crd.Name)
				}
				crdInfos = append(crdInfos, crdInfo{controller: controller, crd: crd, needsWait: false})
				continue
			}
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to check if CRD exists for controller %s: %w", controllerName, err)
			}

			// Create the CRD
			klog.Infof("Installing %s CRD %s", controllerName, crd.Name)
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
				prefix := fmt.Sprintf("/%s/%s", registration.GetName(), endpoint)
				log.V(2).Info("Registering passthrough endpoint",
					"controller", registration.GetName(), "endpoint", prefix)
				mux.Handle(prefix+"/", http.StripPrefix(
					prefix,
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						handler := httpController.GetPassthroughHandler(endpoint)
						if handler == nil {
							http.Error(w, "handler not ready", http.StatusServiceUnavailable)
							return
						}
						handler.ServeHTTP(w, r)
					})))
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
