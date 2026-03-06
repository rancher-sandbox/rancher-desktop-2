// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package external provides the common main function for external API group controllers.
package external

import (
	"context"
	"flag"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// RunControllers is the main function for external API group controllers.
// It handles command line parsing, kubeconfig retrieval, discovery checking, and shared manager setup.
// It returns the command's exit code.
func RunControllers(apiGroupName string) int {
	// Each instance reserves 4 consecutive ports starting at 8080:
	// +0 external metrics, +1 external health, +2 embedded metrics, +3 embedded health.
	instanceOffset := 4 * instance.Index()
	var desiredMetricsPort int
	var desiredHealthPort int

	flag.IntVar(&desiredMetricsPort, "metrics-port", 8080+instanceOffset, "The desired port the metric endpoint binds to.")
	flag.IntVar(&desiredHealthPort, "health-port", 8081+instanceOffset, "The desired port the health probe endpoint binds to.")

	klog.InitFlags(nil)
	//revive:disable-next-line:deep-exit
	flag.Parse()

	log := klog.NewKlogr()
	ctx := klog.NewContext(ctrl.SetupSignalHandler(), log)

	ctrllog.SetLogger(log)
	setupLog := log.WithName("setup")

	// Get Kubernetes configuration from `rdd svc config`
	config, err := base.GetKubeConfigFromRDD(ctx)
	if err != nil {
		setupLog.Error(err, "Failed to get kubeconfig")
		return 1
	}

	// Check which controllers should start and filter the list
	var controllersToStart []base.Controller

	for _, controller := range base.GetAllControllers() {
		shouldStart, err := shouldStartController(ctx, config, controller.GetName(), setupLog)
		if err != nil {
			setupLog.Error(err, "Failed to check for running controller, starting anyway", "controller", controller.GetName())
			controllersToStart = append(controllersToStart, controller)
		} else if shouldStart {
			controllersToStart = append(controllersToStart, controller)
		} else {
			setupLog.Info("Controller is already running in RDD, skipping", "controller", controller.GetName())
		}
	}

	// If no controllers need to start, exit
	if len(controllersToStart) == 0 {
		setupLog.Info("All controllers are already running in RDD controller manager, exiting")
		return 0
	}

	// Create a cancellable context for control plane monitoring
	monitorCtx, cancelMonitor := context.WithCancel(ctx)
	defer cancelMonitor()

	// Start control plane monitoring in background
	var shutdownStartTime atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		monitorControlPlane(monitorCtx, apiGroupName, config, setupLog, func() {
			// Track when shutdown is initiated for timing logs
			shutdownStartTime.Store(time.Now().UnixNano())
			cancelMonitor()
		})
	}()

	// Create shared controller manager for this API group.
	// Start() resolves ports to available ones immediately before binding.
	sharedManager, err := controllers.NewSharedControllerManager(apiGroupName, config, desiredMetricsPort, desiredHealthPort)
	if err != nil {
		setupLog.Error(err, "Failed to create shared controller manager")
		return 1
	}

	// Register only the controllers that should start
	for _, controller := range controllersToStart {
		if err := sharedManager.RegisterController(controller); err != nil {
			setupLog.Error(err, "Failed to register controller", "controller", controller.GetName())
			return 1
		}
	}

	setupLog.Info(fmt.Sprintf("Starting %s controller manager", apiGroupName), "controllers", sharedManager.GetRegisteredControllers())

	// Start the shared manager (this blocks until context is cancelled)
	if err := sharedManager.Start(monitorCtx); err != nil {
		setupLog.Error(err, fmt.Sprintf("Problem running %s controller manager", apiGroupName))
		return 1
	}

	// Log shutdown duration if we have a start time (monitor initiated shutdown)
	if startNanos := shutdownStartTime.Load(); startNanos > 0 {
		shutdownDuration := time.Since(time.Unix(0, startNanos))
		setupLog.Info("Manager shutdown completed", "duration", shutdownDuration.Round(time.Millisecond))
	}

	// Wait for monitoring goroutine to finish
	wg.Wait()
	setupLog.Info("External controller manager shutting down")

	return 0
}

// monitorControlPlane monitors the control plane lifecycle and cancels the context when it's no longer available.
// This allows external controllers to automatically exit when `rdd svc stop` or `rdd svc delete` is called.
func monitorControlPlane(ctx context.Context, apiGroupName string, config *rest.Config, log klog.Logger, cancel context.CancelFunc) {
	// Use a short timeout for monitoring so we detect shutdown quickly.
	// 1 second is sufficient for local API server connections.
	monitorConfig := rest.CopyConfig(config)
	monitorConfig.Timeout = time.Second

	discovery, err := controllers.NewControllerManagerDiscoveryGroup(monitorConfig, apiGroupName)
	if err != nil {
		log.Error(err, "Failed to create discovery service for monitoring")
		return
	}

	// Wait until the external controller has successfully registered in discovery
	// This ensures we don't start monitoring until the controller is actually running
	log.V(1).Info("Waiting for external controller to register in discovery system")

	registered := false
	registrationFailures := 0
	const maxRegistrationFailures = 3 // Exit if API server appears down during registration

	for range 60 { // Wait up to 2 minutes for registration
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}

		info, err := discovery.DiscoverControllerManager(ctx)
		if err == nil && info != nil {
			log.V(1).Info("External controller successfully registered, starting monitoring")
			registered = true
			break
		}

		// Track consecutive failures - if API server is down, exit quickly
		if err != nil {
			registrationFailures++
			log.V(2).Info("Registration check failed",
				"consecutiveFailures", registrationFailures,
				"error", err)
			if registrationFailures >= maxRegistrationFailures {
				log.Info("Control plane appears unavailable during registration - shutting down")
				cancel()
				return
			}
		} else {
			// info == nil but no error means not registered yet, reset counter
			registrationFailures = 0
		}
	}

	if !registered {
		log.Info("External controller failed to register in discovery system, starting monitoring anyway")
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	consecutiveFailures := 0
	const maxFailures = 2 // 4 seconds of failures (2 * 2 seconds) - sufficient for local connections

	log.V(1).Info("Starting control plane monitoring")

	for {
		select {
		case <-ctx.Done():
			log.V(1).Info("Control plane monitoring stopped due to context cancellation")
			return
		case <-ticker.C:
			// Check if the control plane is still running by looking for the discovery ConfigMap
			info, err := discovery.DiscoverControllerManager(ctx)
			if err != nil || info == nil {
				consecutiveFailures++
				log.V(2).Info("Control plane discovery failed",
					"consecutiveFailures", consecutiveFailures,
					"maxFailures", maxFailures,
					"error", err)

				if consecutiveFailures >= maxFailures {
					log.Info("Control plane appears to have stopped - initiating external controller shutdown")
					cancel()
					return
				}
			} else if consecutiveFailures > 0 {
				// Reset failure counter if we successfully discover the control plane
				log.V(2).Info("Control plane discovered successfully, resetting failure counter",
					"previousFailures", consecutiveFailures)
				consecutiveFailures = 0
			}
		}
	}
}

// shouldStartController checks if an external controller should start based on RDD discovery.
// Returns true if no RDD controller manager is running or the specific controller is not enabled.
func shouldStartController(ctx context.Context, config *rest.Config, controllerName string, log klog.Logger) (bool, error) {
	discovery, err := controllers.NewControllerManagerDiscovery(config)
	if err != nil {
		return false, fmt.Errorf("failed to create discovery service: %w", err)
	}

	isRunning, info, err := discovery.IsControllerRunning(ctx, controllerName)
	if err != nil {
		return false, fmt.Errorf("failed to discover controller: %w", err)
	}

	if !isRunning {
		// No controller manager running or controller not enabled
		return true, nil
	}

	// Controller is already running in RDD
	log.Info("Controller is already running in RDD controller manager",
		"controller", controllerName,
		"healthEndpoint", info.HealthEndpoint,
		"metricsEndpoint", info.MetricsEndpoint)

	return false, nil
}
