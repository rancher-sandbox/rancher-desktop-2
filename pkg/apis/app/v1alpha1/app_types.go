// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AppKind is the Kind string for App resources.
const AppKind = "App"

// AppSpec defines the desired state of App.
type AppSpec struct {
	// running specifies whether the VM should be running.
	// +kubebuilder:default=false
	Running bool `json:"running"`
	// Namespace is the namespace where this cluster-scoped App resource
	// creates and manages its owned namespaced resources (e.g., rancher-desktop).
	// Defaults to "default" if not specified.
	// This field is immutable after creation: changing it would orphan existing
	// owned resources (LimaVM, ConfigMaps) in the original namespace.
	// +optional
	// +kubebuilder:default="default"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.namespace is immutable"
	Namespace string `json:"namespace,omitempty"`
}

// AppStatus defines the observed state of App.
type AppStatus struct {
	// conditions represent the current state of the App resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=apps,categories="all"
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'app'",message="App resource must be named 'app'"

// App is the Schema for the apps API.
// This is a cluster-scoped singleton resource - only one instance named 'app' is allowed.
type App struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AppSpec   `json:"spec,omitempty"`
	Status AppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AppList contains a list of App.
type AppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []App `json:"items"`
}

func init() {
	SchemeBuilder.Register(&App{}, &AppList{})
}

// GetResourceNamespace implements the base.ResourceNamespace interface.
// It returns the namespace where this cluster-scoped App resource creates
// and manages its owned namespaced resources.
func (a *App) GetResourceNamespace() string {
	if a.Spec.Namespace != "" {
		return a.Spec.Namespace
	}
	return metav1.NamespaceDefault
}
