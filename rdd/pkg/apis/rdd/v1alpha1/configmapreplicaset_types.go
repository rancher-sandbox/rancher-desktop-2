// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConfigMapSpec defines the desired state of ConfigMapReplicaSet.
// This spec allows users to specify how many ConfigMaps should be created
// and what data they should contain.
type ConfigMapSpec struct {
	// Data contains the key-value pairs that will be stored in each ConfigMap.
	// This data is replicated across all managed ConfigMaps with an additional
	// "index" field added to distinguish each replica.
	Data map[string]string `json:"data,omitempty"`

	// Replicas specifies the desired number of ConfigMaps to maintain.
	// The controller will create, update, or delete ConfigMaps to match this count.
	// If not specified, defaults to 1.
	Replicas *int32 `json:"replicas,omitempty"`
}

// ConfigMapStatus defines the observed state of ConfigMapReplicaSet.
// This status reflects the current state of the managed ConfigMaps.
type ConfigMapStatus struct {
	// ReadyReplicas indicates the number of ConfigMaps that have been
	// successfully created and are currently managed by this controller.
	ReadyReplicas int32 `json:"readyReplicas"`

	// Conditions represent the latest available observations of the
	// ConfigMapReplicaSet's current state. This can be used to communicate
	// detailed status information to users and other controllers.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=cmrs

// ConfigMapReplicaSet is the Schema for the configmapreplicasets API.
// This custom resource allows users to declaratively manage multiple ConfigMaps
// without requiring any pod or container orchestration. It demonstrates a
// minimalist controller pattern that operates purely at the Kubernetes API level.
//
// Example usage:
//
//	apiVersion: rdd.rancherdesktop.io/v1alpha1
//	kind: ConfigMapReplicaSet
//	metadata:
//	  name: my-config
//	spec:
//	  replicas: 3
//	  data:
//	    config.yaml: |
//	      key: value
//
// ConfigMapReplicaSet is the Schema for the configmapreplicasets API.
type ConfigMapReplicaSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigMapSpec   `json:"spec,omitempty"`
	Status ConfigMapStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ConfigMapReplicaSetList contains a list of ConfigMapReplicaSet resources.
// This type is used by the Kubernetes API for listing operations.
type ConfigMapReplicaSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConfigMapReplicaSet `json:"items"`
}

// init registers the ConfigMapReplicaSet and ConfigMapReplicaSetList types
// with the scheme builder, making them available to the Kubernetes API machinery.
func init() {
	registerTypes(&ConfigMapReplicaSet{}, &ConfigMapReplicaSetList{})
}
