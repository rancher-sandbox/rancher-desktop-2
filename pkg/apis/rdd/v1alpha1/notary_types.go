// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotarySpec defines the desired state of Notary.
type NotarySpec struct {
	// Value is the field that will be tracked for changes
	Value string `json:"value"`

	// ConfigMapName is the name of the ConfigMap where the history will be stored
	ConfigMapName string `json:"configMapName"`
}

// NotaryStatus defines the observed state of Notary.
type NotaryStatus struct {
	// LastRecordedValue is the last value that was recorded in the ConfigMap
	LastRecordedValue string `json:"lastRecordedValue,omitempty"`

	// ConfigMapStatus indicates the status of the ConfigMap operation
	ConfigMapStatus string `json:"configMapStatus,omitempty"`

	// ChangeCount is the number of changes recorded so far
	ChangeCount int `json:"changeCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:subresource:status

// Notary is the Schema for the notaries API.
type Notary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NotarySpec   `json:"spec,omitempty"`
	Status NotaryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NotaryList contains a list of Notary.
type NotaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Notary `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Notary{}, &NotaryList{})
}
