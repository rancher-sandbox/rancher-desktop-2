// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Demo condition types.
const (
	// DemoConditionReady indicates whether the demo controller is ready to process messages.
	DemoConditionReady = "Ready"
	// DemoConditionProcessing indicates whether the demo controller is actively processing.
	DemoConditionProcessing = "Processing"
	// DemoConditionCompleted indicates whether the demo controller has finished processing all messages.
	DemoConditionCompleted = "Completed"
)

// DemoSpec defines the desired state of Demo.
type DemoSpec struct {
	// Message is the message to process
	Message string `json:"message,omitempty"`
	// Count is the number of times to process the message
	Count int32 `json:"count,omitempty"`
	// Namespace is the namespace where this cluster-scoped Demo resource
	// creates and manages its owned namespaced resources (e.g., LimaVMs).
	// Defaults to "default" if not specified.
	// +optional
	// +kubebuilder:default="default"
	Namespace string `json:"namespace,omitempty"`
}

// DemoStatus defines the observed state of Demo.
type DemoStatus struct {
	// ProcessedCount is the number of times the message has been processed
	ProcessedCount int32 `json:"processedCount,omitempty"`
	// LastProcessed is the timestamp when the message was last processed
	LastProcessed string `json:"lastProcessed,omitempty"`
	// Conditions represent the latest available observations of the demo's current state
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=demos
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'demo'",message="Demo resource must be named 'demo'"

// Demo is the Schema for the demos API.
// This is a cluster-scoped singleton resource - only one instance named 'demo' is allowed.
type Demo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DemoSpec   `json:"spec,omitempty"`
	Status DemoStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DemoList contains a list of Demo.
type DemoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Demo `json:"items"`
}

func init() {
	registerTypes(&Demo{}, &DemoList{})
}

// GetResourceNamespace implements the base.ResourceNamespace interface.
// It returns the namespace where this cluster-scoped Demo resource creates
// and manages its owned namespaced resources.
func (d *Demo) GetResourceNamespace() string {
	if d.Spec.Namespace != "" {
		return d.Spec.Namespace
	}
	return metav1.NamespaceDefault
}
