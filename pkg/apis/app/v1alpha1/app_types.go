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

// KubernetesControllerName is the registry name of the Kubernetes context controller.
const KubernetesControllerName = "kubernetes"

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

	// AppConditionKubernetesReady goes True once the Kubernetes controller has
	// confirmed that the k3s API server is reachable and has merged the
	// rancher-desktop-{instance} context into ~/.kube/config. It is only
	// meaningful when spec.kubernetes.enabled is true; when Kubernetes is
	// disabled the condition is absent or False with reason NotApplicable.
	AppConditionKubernetesReady = "KubernetesReady"

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

	// AppSettledReasonWaitingForKubernetes means the Kubernetes controller has not yet written KubernetesReady.
	AppSettledReasonWaitingForKubernetes = "WaitingForKubernetes"

	// AppSettledReasonKubernetesStale means the Kubernetes controller has not yet observed the current generation.
	AppSettledReasonKubernetesStale = "KubernetesStale"
)

// Reasons for the KubernetesReady condition.
const (
	// AppKubernetesReasonReady means the k3s API server is reachable and the
	// kubeconfig context has been merged into ~/.kube/config.
	AppKubernetesReasonReady = "Ready"

	// AppKubernetesReasonNotApplicable means spec.kubernetes.enabled is false;
	// the condition is set to False with this reason so consumers can
	// distinguish "disabled" from "still starting".
	AppKubernetesReasonNotApplicable = "NotApplicable"

	// AppKubernetesReasonNotRunning means the VM is not running, so k3s
	// cannot be healthy.
	AppKubernetesReasonNotRunning = "NotRunning"

	// AppKubernetesReasonProbing means the controller is still waiting for
	// the k3s API server to respond.
	AppKubernetesReasonProbing = "Probing"
)

const (
	// EngineReasonStopped is set on ContainerEngineReady when the engine has
	// stopped and all mirror resources have been cleaned up.
	EngineReasonStopped = "Stopped"

	// EngineReasonNotApplicable is set on ContainerEngineReady for backends
	// (e.g. containerd) that do not use Docker mirroring.
	EngineReasonNotApplicable = "NotApplicable"

	// EngineReasonConnected is set on ContainerEngineReady when the engine is
	// running and mirror resources are in sync.
	EngineReasonConnected = "Connected"

	// EngineReasonConnectFailed is set on ContainerEngineReady when the engine
	// reconciler could not connect to the Docker daemon.
	EngineReasonConnectFailed = "ConnectFailed"
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
	// kubernetesPort is the intended port for the Kubernetes API server.
	// AppReconciler calls ResolvePort to find a free port, closes the
	// listener, then persists this value. Lima's identity port-forward rule
	// (guestPortRange:[1,65535] → hostPortRange:[0,0]) later binds the same
	// port on the host.
	//
	// The window between ResolvePort releasing the port and Lima binding it
	// spans VM boot, provisioning, and k3s install — minutes on a cold
	// start. If another process claims the port during that window, Lima
	// logs "failed to set up forwarding tcp port" and kubectl gets
	// connection refused; the LimaVM still reports Running. A future
	// improvement would keep the listener open until Lima is ready to bind,
	// or read the bound host port from Lima state instead of storing an
	// intent.
	// +optional
	KubernetesPort int `json:"kubernetesPort,omitempty"`
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
