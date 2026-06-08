// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

// Controller defines the interface that all RDD controllers must implement.
// This interface supports both external and embedded execution modes.
type Controller interface {
	// GetName returns the controller name for logging and identification
	GetName() string

	// GetAPIGroup returns the API group this controller belongs to
	GetAPIGroup() string

	// GetCRDData returns the embedded CRD YAML data
	GetCRDData() string

	// RegisterWithManager provides complete controller registration including scheme and setup
	RegisterWithManager(mgr ctrl.Manager) error
}

// WebhookController is an optional interface that controllers can implement
// to receive the webhook port allocated by SharedControllerManager and
// participate in shared webhook certificate management.
type WebhookController interface {
	// SetWebhookPort provides the actual webhook port to the controller
	SetWebhookPort(port int)

	// GetWebhookServiceName returns the DNS service name that should be
	// included in the shared webhook certificate SANs. This will be expanded
	// to include full Kubernetes service FQDNs (e.g., "service", "service.default",
	// "service.default.svc", "service.default.svc.cluster.local").
	GetWebhookServiceName() string

	// GetWebhookManagers returns all WebhookManagers for parallel setup.
	// Returns nil or empty slice if the controller doesn't use webhooks.
	GetWebhookManagers() []WebhookManager
}

// PassthroughController is an optional interface that controllers can implement
// to provide custom HTTP handlers.  These handlers will be exposed on the API
// server under the `/passthrough/<controller>/<endpoint>` path.
//
// This is intended for controllers that need to provide more complex HTTP
// functionality that cannot be provided using custom resources, such as
// handling WebSocket connections to stream data.
type PassthroughController interface {
	// GetPassthroughEndpoints returns the list of HTTP endpoints provided by
	// this controller.  These endpoints will be exposed on the API server under
	// the `/passthrough/<controller>/<endpoint>/` path; for example, an
	// endpoint named "logs" on the "example" controller will be accessible at
	// `/passthrough/example/logs/`.  The endpoint names returned must not
	// contain duplicates, and must be a valid path segment (i.e., no slashes).
	GetPassthroughEndpoints() []string

	// GetPassthroughHandler returns the HTTP handler for the given endpoint.
	// The handler does not get the `/passthrough/<controller>/<endpoint>`
	// prefix; for example, for an endpoint "logs" on the "example" controller,
	// if the client requests `/passthrough/example/logs/id` the handler will
	// receive requests to `/id`.
	GetPassthroughHandler(endpoint string) http.Handler
}

// Registry holds all registered controllers.
type Registry struct {
	mu          sync.RWMutex
	controllers []Controller
}

// Global registry instance.
var defaultRegistry = &Registry{
	controllers: make([]Controller, 0),
}

// RegisterController registers a controller with the global registry.
// This function is called by controller init functions.
func RegisterController(controller Controller) {
	defaultRegistry.Register(controller)
}

// Register adds a controller to the registry.
func (r *Registry) Register(controller Controller) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.controllers = append(r.controllers, controller)
	klog.V(2).Infof("Registered controller %s in API group %s", controller.GetName(), controller.GetAPIGroup())
}

// GetAllControllers returns all registered controllers as a slice.
func (r *Registry) GetAllControllers() []Controller {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Controller, len(r.controllers))
	copy(result, r.controllers)
	return result
}

// GetAllControllers returns all registered controllers as a slice using the global registry.
func GetAllControllers() []Controller {
	return defaultRegistry.GetAllControllers()
}

// GetKubeConfigFromRDD returns the Kubernetes configuration by running `rdd svc config`.
// This function is used by external controllers to retrieve kubeconfig dynamically.
func GetKubeConfigFromRDD(ctx context.Context) (*rest.Config, error) {
	// Get kubeconfig from rdd svc config command
	kubeconfigYAML, err := getRDDKubeConfig(ctx)
	if err != nil {
		return nil, err
	}

	// Parse the YAML kubeconfig
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigYAML)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// getRDDKubeConfig executes `rdd svc config` and returns the kubeconfig YAML.
// It first tries to find rdd on PATH, then looks in the same directory as the current executable.
func getRDDKubeConfig(ctx context.Context) ([]byte, error) {
	exeName := "rdd"
	if runtime.GOOS == "windows" {
		exeName = "rdd.exe"
	}
	// First try to find rdd on PATH
	rddPath, err := exec.LookPath(exeName)
	if err != nil {
		// If not found on PATH, look in the same directory as this executable
		execPath, execErr := os.Executable()
		if execErr != nil {
			return nil, execErr
		}
		rddPath = filepath.Join(filepath.Dir(execPath), exeName)

		// Check if rdd exists in the same directory
		if _, statErr := os.Stat(rddPath); statErr != nil {
			return nil, err // Return the original LookPath error
		}
	}

	// Execute rdd svc config command
	cmd := exec.CommandContext(ctx, rddPath, "svc", "config")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return output, nil
}
