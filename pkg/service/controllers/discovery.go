// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	// ControllerManagerConfigMapName is the name of the ConfigMap that stores controller manager information.
	ControllerManagerConfigMapName = "rdd-controller-manager"

	// RDDSystemNamespace is the namespace where RDD stores its control plane information.
	RDDSystemNamespace = "rdd-system"
)

// ControllerManagerInput contains the input parameters for registering a controller manager.
type ControllerManagerInput struct {
	HealthPort          int                 `json:"healthPort"`
	MetricsPort         int                 `json:"metricsPort"`
	PassthroughPort     int                 `json:"-"`
	EnabledControllers  []string            `json:"enabledControllers"`
	EnabledPassthroughs map[string][]string `json:"enabledPassthroughs"`
}

// ControllerManagerInfo contains discovered information about a running controller manager.
type ControllerManagerInfo struct {
	ControllerManagerInput `json:",inline"`
	StartTime              metav1.Time `json:"startTime"`
	HealthEndpoint         string      `json:"healthEndpoint"`
	MetricsEndpoint        string      `json:"metricsEndpoint"`
	PassthroughEndpoint    string      `json:"passthroughEndpoint"`
}

// ControllerManagerDiscovery handles service discovery for the shared controller manager.
type ControllerManagerDiscovery struct {
	client    kubernetes.Interface
	namespace string
}

// ControllerManagerDiscoveryGroup handles service discovery for a specific API
// group.
type ControllerManagerDiscoveryGroup struct {
	ControllerManagerDiscovery
	name string
}

// NewControllerManagerDiscovery creates a new read-only discovery service.
func NewControllerManagerDiscovery(config *rest.Config) (*ControllerManagerDiscovery, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &ControllerManagerDiscovery{
		client:    client,
		namespace: RDDSystemNamespace,
	}, nil
}

// NewControllerManagerDiscoveryGroup creates a new discovery service that can
// register and unregister controller managers for a given API group.
func NewControllerManagerDiscoveryGroup(config *rest.Config, apiGroupName string) (*ControllerManagerDiscoveryGroup, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &ControllerManagerDiscoveryGroup{
		ControllerManagerDiscovery: ControllerManagerDiscovery{
			client:    client,
			namespace: RDDSystemNamespace,
		},
		name: apiGroupName,
	}, nil
}

// RegisterControllerManager creates or updates the ConfigMap with controller manager information.
func (d *ControllerManagerDiscoveryGroup) RegisterControllerManager(ctx context.Context, input ControllerManagerInput) error {
	return d.registerControllerManagerImpl(ctx, input, true)
}

// registerControllerManagerImpl is the internal implementation of RegisterControllerManager,
// with an option to retry on conflict.
func (d *ControllerManagerDiscoveryGroup) registerControllerManagerImpl(ctx context.Context, input ControllerManagerInput, allowRetry bool) error {
	if err := d.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	info := ControllerManagerInfo{
		ControllerManagerInput: input,
		StartTime:              metav1.NewTime(time.Now().UTC()),
		HealthEndpoint:         fmt.Sprintf("http://localhost:%d/healthz", input.HealthPort),
		MetricsEndpoint:        fmt.Sprintf("http://localhost:%d/metrics", input.MetricsPort),
		PassthroughEndpoint:    fmt.Sprintf("http://localhost:%d/", input.PassthroughPort),
	}
	serializedInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("failed to serialize controller manager info: %w", err)
	}

	// Update the config map if it exists.
	patchData, err := json.Marshal(map[string]any{
		"data": map[string]string{
			d.name: string(serializedInfo),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize controller manager patch: %w", err)
	}
	_, err = d.client.CoreV1().ConfigMaps(d.namespace).Patch(
		ctx,
		ControllerManagerConfigMapName,
		types.StrategicMergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	if err == nil {
		klog.InfoS("Registered controller manager in cluster",
			"namespace", d.namespace,
			"configmap", ControllerManagerConfigMapName,
			"name", d.name,
			"controllers", len(info.EnabledControllers))
		return nil
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to patch existing controller manager configmap: %w", err)
	}

	// Create the config map
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ControllerManagerConfigMapName,
			Namespace: d.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "rancher-desktop-daemon",
				"app.kubernetes.io/component": "controller-manager",
			},
		},
		Data: map[string]string{
			d.name: string(serializedInfo),
		},
	}
	_, err = d.client.CoreV1().ConfigMaps(d.namespace).Create(ctx, configMap, metav1.CreateOptions{})
	if err != nil {
		if allowRetry && errors.IsAlreadyExists(err) {
			// We hit a race condition, and somebody else created it first.  Try again.
			// We're not expecting to recurse more than once, because if the config map
			// exists then we won't hit IsNotFound on the patch attempt.
			return d.registerControllerManagerImpl(ctx, input, false)
		}
		return fmt.Errorf("failed to register controller manager: %w", err)
	}

	klog.InfoS("Registered initial controller manager in cluster",
		"namespace", d.namespace,
		"configmap", ControllerManagerConfigMapName,
		"name", d.name,
		"controllers", len(info.EnabledControllers))

	return nil
}

// UnregisterControllerManager removes the controller manager ConfigMap.
func (d *ControllerManagerDiscoveryGroup) UnregisterControllerManager(ctx context.Context) error {
	patchData, err := json.Marshal(map[string]any{
		"data": map[string]any{
			d.name: nil,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to serialize controller manager patch: %w", err)
	}

	cm, err := d.client.CoreV1().ConfigMaps(d.namespace).Patch(
		ctx,
		ControllerManagerConfigMapName,
		types.StrategicMergePatchType,
		patchData,
		metav1.PatchOptions{},
	)
	if errors.IsNotFound(err) {
		return nil // Already deleted, no error
	}
	if err != nil {
		return fmt.Errorf("failed to patch controller manager configmap: %w", err)
	}

	if len(cm.Data) == 0 {
		// No more entries, delete the ConfigMap
		err = d.client.CoreV1().ConfigMaps(d.namespace).Delete(
			ctx,
			ControllerManagerConfigMapName,
			metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{
					ResourceVersion: &cm.ResourceVersion,
				},
			},
		)

		if err == nil {
			klog.InfoS("Unregistered final controller manager from cluster",
				"namespace", d.namespace,
				"configmap", ControllerManagerConfigMapName,
				"name", d.name)
			return nil
		}

		// If somebody else deleted the config map or modified it, the config
		// map should stay in the state the other controller managers left it.
		if !errors.IsNotFound(err) && !errors.IsConflict(err) {
			return fmt.Errorf("failed to delete controller manager configmap: %w", err)
		}
	}

	klog.InfoS("Unregistered controller manager from cluster",
		"namespace", d.namespace,
		"configmap", ControllerManagerConfigMapName,
		"name", d.name)

	return nil
}

// DiscoverControllerManager finds running controller manager information.
func (d *ControllerManagerDiscoveryGroup) DiscoverControllerManager(ctx context.Context) (*ControllerManagerInfo, error) {
	configMap, err := d.client.CoreV1().ConfigMaps(d.namespace).Get(ctx, ControllerManagerConfigMapName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil, nil // No controller manager running
	}
	if err != nil {
		return nil, fmt.Errorf("failed to discover controller manager: %w", err)
	}

	serializedInfo, exists := configMap.Data[d.name]
	if !exists {
		return nil, nil // No entry for this instance
	}

	var info ControllerManagerInfo
	if err := json.Unmarshal([]byte(serializedInfo), &info); err != nil {
		return nil, fmt.Errorf("failed to parse controller manager info: %w", err)
	}
	return &info, nil
}

// GetEnabledControllers returns the list of all enabled controllers, across all
// controller managers.  Note that the returned controllers may not be running.
func (d *ControllerManagerDiscovery) GetEnabledControllers(ctx context.Context) ([]string, error) {
	configMap, err := d.client.CoreV1().ConfigMaps(d.namespace).Get(ctx, ControllerManagerConfigMapName, metav1.GetOptions{})
	if errors.IsNotFound(err) {
		return nil, nil // No controller manager running
	}
	if err != nil {
		return nil, fmt.Errorf("failed to discover controller manager: %w", err)
	}

	enabledControllers := make([]string, 0) // non-nil: distinguishes "0 controllers" from "ConfigMap not found"
	for _, serializedInfo := range configMap.Data {
		var info ControllerManagerInfo
		if err := json.Unmarshal([]byte(serializedInfo), &info); err != nil {
			return nil, fmt.Errorf("failed to parse controller manager info: %w", err)
		}

		enabledControllers = append(enabledControllers, info.EnabledControllers...)
	}

	return enabledControllers, nil
}

// IsControllerRunning checks if a specific controller is running in any of the
// shared controller managers.
func (d *ControllerManagerDiscovery) IsControllerRunning(ctx context.Context, controllerName string) (bool, *ControllerManagerInfo, error) {
	configMap, err := d.client.CoreV1().ConfigMaps(d.namespace).Get(ctx, ControllerManagerConfigMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil, nil // No controller manager running
		}
		return false, nil, fmt.Errorf("failed to discover controller manager: %w", err)
	}

	for _, serializedInfo := range configMap.Data {
		var info ControllerManagerInfo
		if err := json.Unmarshal([]byte(serializedInfo), &info); err != nil {
			return false, nil, fmt.Errorf("failed to parse controller manager info: %w", err)
		}

		if !slices.Contains(info.EnabledControllers, controllerName) {
			continue // This controller manager does not have the controller enabled.
		}

		// Check if the controller manager is actually accessible
		return d.isControllerManagerHealthy(ctx, &info), &info, nil
	}

	return false, nil, nil // Controller not found in any controller manager.
}

// LookupPassthroughEndpoint looks up the endpoint URL for a given passthrough
// endpoint name across all controller managers.  If the endpoint is not found,
// an empty string is returned.
func (d *ControllerManagerDiscovery) LookupPassthroughEndpoint(ctx context.Context, controllerName, endpointName string) (string, error) {
	configMap, err := d.client.CoreV1().ConfigMaps(d.namespace).Get(ctx, ControllerManagerConfigMapName, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil // No controller manager running
		}
		return "", fmt.Errorf("failed to discover controller manager: %w", err)
	}

	for _, serializedInfo := range configMap.Data {
		var info ControllerManagerInfo
		if err := json.Unmarshal([]byte(serializedInfo), &info); err != nil {
			return "", fmt.Errorf("failed to parse controller manager info: %w", err)
		}

		enabledPassthroughs := info.EnabledPassthroughs[controllerName]
		if !slices.Contains(enabledPassthroughs, endpointName) {
			continue
		}

		// Check if the controller manager is actually accessible
		if d.isControllerManagerHealthy(ctx, &info) {
			return info.PassthroughEndpoint, nil
		}
	}

	return "", nil // Endpoint not found in any controller manager.
}

// isControllerManagerHealthy checks if the controller manager is responding to health checks.
func (d *ControllerManagerDiscovery) isControllerManagerHealthy(ctx context.Context, info *ControllerManagerInfo) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.HealthEndpoint, http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ensureNamespace creates the rdd-system namespace if it doesn't exist.
func (d *ControllerManagerDiscovery) ensureNamespace(ctx context.Context) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: d.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "rancher-desktop-daemon",
			},
		},
	}

	_, err := d.client.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		return nil // Namespace already exists, that's fine
	}
	return err
}

// InitDiscovery deletes any stale discovery ConfigMap and creates an empty one.
// The ConfigMap's creationTimestamp serves as the control plane start time.
// This must be called after the API server is ready and before any controller
// managers register.
func InitDiscovery(ctx context.Context, client kubernetes.Interface) error {
	d := &ControllerManagerDiscovery{client: client, namespace: RDDSystemNamespace}
	if err := d.ensureNamespace(ctx); err != nil {
		return fmt.Errorf("failed to ensure namespace: %w", err)
	}

	// Delete stale ConfigMap from a previous run (e.g. unclean shutdown).
	err := client.CoreV1().ConfigMaps(RDDSystemNamespace).Delete(ctx, ControllerManagerConfigMapName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete stale discovery configmap: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ControllerManagerConfigMapName,
			Namespace: RDDSystemNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "rancher-desktop-daemon",
				"app.kubernetes.io/component": "controller-manager",
			},
		},
	}
	if _, err := client.CoreV1().ConfigMaps(RDDSystemNamespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create discovery configmap: %w", err)
	}

	klog.InfoS("Initialized discovery configmap",
		"namespace", RDDSystemNamespace,
		"configmap", ControllerManagerConfigMapName)
	return nil
}

// CleanupDiscovery deletes the controller manager discovery ConfigMap.
func CleanupDiscovery(ctx context.Context, client *kubernetes.Clientset) error {
	err := client.CoreV1().ConfigMaps(RDDSystemNamespace).Delete(ctx, ControllerManagerConfigMapName, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil // Already deleted, no error
	}
	if err != nil {
		return fmt.Errorf("failed to delete controller manager configmap: %w", err)
	}

	klog.InfoS("Cleaned up stale controller manager discovery configmap",
		"namespace", RDDSystemNamespace,
		"configmap", ControllerManagerConfigMapName)

	return nil
}
