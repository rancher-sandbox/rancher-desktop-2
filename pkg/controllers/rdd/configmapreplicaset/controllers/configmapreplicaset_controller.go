// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the reconciliation logic for custom resources.
// This controller manages ConfigMaps declaratively without any pod dependencies.
package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// ConfigMapReplicaSetReconciler reconciles a ConfigMapReplicaSet object.
// This reconciler implements the core logic for managing ConfigMap replicas
// based on the desired state specified in ConfigMapReplicaSet resources.
// It operates without any pod or container dependencies, demonstrating a
// minimalist controller pattern.
type ConfigMapReplicaSetReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Manager ctrl.Manager
}

//+kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=configmapreplicasets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=configmapreplicasets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=configmapreplicasets/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// The reconciliation logic:
// 1. Fetches the ConfigMapReplicaSet resource
// 2. Determines the desired number of ConfigMap replicas
// 3. Lists existing ConfigMaps managed by this controller
// 4. Scales up by creating new ConfigMaps if current < desired
// 5. Scales down by deleting excess ConfigMaps if current > desired
// 6. Updates the status to reflect the current number of ready replicas
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *ConfigMapReplicaSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the ConfigMapReplicaSet instance
	var configMapReplicaSet v1alpha1.ConfigMapReplicaSet
	if err := r.Get(ctx, req.NamespacedName, &configMapReplicaSet); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ConfigMapReplicaSet resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ConfigMapReplicaSet")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if base.IsBeingDeleted(&configMapReplicaSet) {
		return r.handleDeletion(ctx, &configMapReplicaSet)
	}

	// Add finalizer if not present
	if added, err := base.EnsureCleanupFinalizer(ctx, r.Client, &configMapReplicaSet); err != nil {
		logger.Error(err, "Failed to add finalizer")
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{}, nil
	}

	// Get desired replica count (default to 1 if not specified)
	replicas := int32(1)
	if configMapReplicaSet.Spec.Replicas != nil {
		replicas = *configMapReplicaSet.Spec.Replicas
	}

	// List existing ConfigMaps managed by this controller
	var configMapList corev1.ConfigMapList
	listOpts := []client.ListOption{
		client.InNamespace(configMapReplicaSet.Namespace),
		client.MatchingLabels(labelsForConfigMap(configMapReplicaSet.Name)),
	}

	if err := r.List(ctx, &configMapList, listOpts...); err != nil {
		logger.Error(err, "Failed to list ConfigMaps")
		return ctrl.Result{}, err
	}

	// Current number of ConfigMaps
	currentReplicas := int32(len(configMapList.Items))

	// Scale up if needed
	if currentReplicas < replicas {
		for i := currentReplicas; i < replicas; i++ {
			configMap, err := r.configMapForController(&configMapReplicaSet, i)
			if err != nil {
				logger.Error(err, "Failed to get config map for controller", "ConfigMapReplicaSet.Namespace", configMapReplicaSet.Namespace, "ConfigMapReplicaSet.Name", configMapReplicaSet.Name)
				return ctrl.Result{}, err
			}
			logger.Info("Creating ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			if err := r.Create(ctx, configMap); err != nil {
				logger.Error(err, "Failed to create new ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
				return ctrl.Result{}, err
			}
		}
	}

	// Scale down if needed
	if currentReplicas > replicas {
		for i := replicas; i < currentReplicas; i++ {
			configMapName := fmt.Sprintf("%s-%d", configMapReplicaSet.Name, i)
			configMap := &corev1.ConfigMap{}
			err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: configMapReplicaSet.Namespace}, configMap)
			if err != nil && errors.IsNotFound(err) {
				continue
			} else if err != nil {
				logger.Error(err, "Failed to get ConfigMap")
				return ctrl.Result{}, err
			}

			logger.Info("Deleting ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
			if err := r.Delete(ctx, configMap); err != nil {
				logger.Error(err, "Failed to delete ConfigMap", "ConfigMap.Namespace", configMap.Namespace, "ConfigMap.Name", configMap.Name)
				return ctrl.Result{}, err
			}
		}
	}

	// Update status
	configMapReplicaSet.Status.ReadyReplicas = replicas
	if err := r.Status().Update(ctx, &configMapReplicaSet); err != nil {
		logger.Error(err, "Failed to update ConfigMapReplicaSet status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// handleDeletion handles cleanup when a ConfigMapReplicaSet is being deleted.
// It removes all owned ConfigMaps and then removes the finalizer to allow
// the ConfigMapReplicaSet to be deleted.
func (r *ConfigMapReplicaSetReconciler) handleDeletion(ctx context.Context, configMapReplicaSet *v1alpha1.ConfigMapReplicaSet) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Clean up all owned ConfigMaps using the helper
	if err := base.DeleteOwnedResources(ctx, r.Client, configMapReplicaSet, r.Manager); err != nil {
		logger.Error(err, "Failed to delete owned resources")
		return ctrl.Result{}, err
	}

	// Remove finalizer to allow deletion
	if err := base.RemoveCleanupFinalizer(ctx, r.Client, configMapReplicaSet); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully cleaned up ConfigMaps and removed finalizer")
	return ctrl.Result{}, nil
}

// configMapForController returns a ConfigMap object for the given ConfigMapReplicaSet.
// This function creates a new ConfigMap with:
// - A unique name based on the controller name and index
// - Labels for identification and selection
// - Data from the controller spec plus an index field
// - Controller reference for garbage collection.
func (r *ConfigMapReplicaSetReconciler) configMapForController(m *v1alpha1.ConfigMapReplicaSet, index int32) (*corev1.ConfigMap, error) {
	labels := labelsForConfigMap(m.Name)
	configMapName := fmt.Sprintf("%s-%d", m.Name, index)

	data := make(map[string]string)
	if m.Spec.Data != nil {
		for k, v := range m.Spec.Data {
			data[k] = v
		}
	}
	data["index"] = fmt.Sprintf("%d", index)

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: m.Namespace,
			Labels:    labels,
		},
		Data: data,
	}

	if err := ctrl.SetControllerReference(m, configMap, r.Scheme); err != nil {
		return nil, err
	}
	return configMap, nil
}

// labelsForConfigMap returns the labels for selecting the resources.
// These labels follow Kubernetes recommended label conventions and allow
// the controller to identify and manage its ConfigMaps efficiently.
func labelsForConfigMap(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "configmap",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/component":  "controller",
		"app.kubernetes.io/managed-by": "rdd-configmapreplicaset",
	}
}

// SetupWithManager sets up the controller with the Manager.
// This configures the controller to:
// - Watch for changes to ConfigMapReplicaSet resources (primary resource)
// - Watch for changes to ConfigMaps that are owned by ConfigMapReplicaSet resources
// - Automatically trigger reconciliation when either type of resource changes.
func (r *ConfigMapReplicaSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ConfigMapReplicaSet{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
