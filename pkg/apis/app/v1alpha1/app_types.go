// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AppKind is the Kind string for App resources.
const AppKind = "App"

// EngineControllerName is the registry name of the engine controller.
// Both the engine controller's own registration and the App reconciler's
// discovery query reference this constant so they cannot drift.
const EngineControllerName = "engine"

// App condition types.
const (
	// AppConditionRunning mirrors the LimaVM Running condition: True
	// means the Lima guest has finished booting and SSH is reachable.
	// It says nothing about the container engine socket; consumers
	// that depend on the engine must also check
	// AppConditionContainerEngineReady.
	AppConditionRunning = "Running"

	// AppConditionContainerEngineReady goes True once the engine
	// controller has connected to the container engine socket and
	// completed its initial full sync of Container, Image, and Volume
	// mirrors. The engine controller stamps the App's generation into
	// ObservedGeneration, so `rdd set` can distinguish a stale True
	// from a fresh one.
	AppConditionContainerEngineReady = "ContainerEngineReady"

	// AppConditionSettled reports whether the reconcile chain has
	// fully caught up with the current spec: observed generations on
	// the feeding conditions match the App's generation, and the VM
	// and engine have reached a stable state for the desired config.
	// A spec change forces Settled to False; once the chain quiesces,
	// the App reconciler flips it back to True. `rdd set` waits on
	// this condition.
	AppConditionSettled = "Settled"
)

// Reasons for the Settled condition. Consumers branch on these
// values; the App reconciler also forwards the Running condition's
// reason when LimaVM has not yet reached the desired state (see
// api_app.md).
const (
	// AppSettledReasonSettled means the App has reached the desired state.
	AppSettledReasonSettled = "Settled"

	// AppSettledReasonWaitingForLimaVM means LimaVM has not yet reported a Running condition.
	AppSettledReasonWaitingForLimaVM = "WaitingForLimaVM"

	// AppSettledReasonWaitingForEngine means the engine controller has not yet written ContainerEngineReady.
	AppSettledReasonWaitingForEngine = "WaitingForEngine"

	// AppSettledReasonEngineStale means the engine controller has not yet observed the current generation.
	AppSettledReasonEngineStale = "EngineStale"
)

// ContainerEngineSpec defines the desired container engine configuration.
type ContainerEngineSpec struct {
	// name specifies the container engine to use.
	// Valid values are "moby" (Docker-compatible) and "containerd".
	// +kubebuilder:validation:Enum=moby;containerd
	// +kubebuilder:default=moby
	Name string `json:"name"`
}

// KubernetesSpec defines the desired Kubernetes configuration.
type KubernetesSpec struct {
	// enabled specifies whether Kubernetes should be enabled in the VM.
	Enabled bool `json:"enabled"`
	// version is the Kubernetes version to use (e.g. "1.32.2").
	// +optional
	Version string `json:"version,omitempty"`
}

// AppSpec defines the desired state of App.
type AppSpec struct {
	// running specifies whether the VM should be running.
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
	// containerEngine specifies the container engine configuration.
	// +optional
	// +kubebuilder:default={name:"moby"}
	ContainerEngine ContainerEngineSpec `json:"containerEngine,omitempty"`
	// kubernetes specifies the Kubernetes configuration.
	// +optional
	Kubernetes KubernetesSpec `json:"kubernetes,omitempty"`
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
