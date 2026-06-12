// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:shortName=cns,categories="all"

// ContainerNamespace defines a container engine namespace; note that this is distinct
// from Kubernetes namespaces.
type ContainerNamespace struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:object:generate=true

// ContainerNamespaceList contains a list of [ContainerNamespace].
type ContainerNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerNamespace `json:"items"`
}

func init() {
	registerTypes(
		&ContainerNamespace{}, &ContainerNamespaceList{},
	)
}
