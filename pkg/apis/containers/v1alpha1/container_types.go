// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=created;running;pausing;paused;restarting;removing;exited;dead;unknown

// ContainerStatusValue describes the status of a container.
type ContainerStatusValue string

const (
	ContainerStatusCreated    ContainerStatusValue = "created"
	ContainerStatusRunning    ContainerStatusValue = "running"
	ContainerStatusPausing    ContainerStatusValue = "pausing"
	ContainerStatusPaused     ContainerStatusValue = "paused"
	ContainerStatusRestarting ContainerStatusValue = "restarting"
	ContainerStatusRemoving   ContainerStatusValue = "removing"
	ContainerStatusExited     ContainerStatusValue = "exited"
	ContainerStatusDead       ContainerStatusValue = "dead"
	ContainerStatusUnknown    ContainerStatusValue = "unknown"
)

// ContainerPortBinding describes one host port for the container to bind to.
type ContainerPortBinding struct {
	// HostIP is the host IP address that the container's port is mapped to.
	//
	// +required
	HostIP string `json:"hostIP"`
	// HostPort is the host port number that the container's port is mapped to.
	//
	// +required
	HostPort string `json:"hostPort"`
}

// ContainerPort defines a single exposed port in a container.
type ContainerPort struct {
	// Name of the port; in the form [port]/[protocol], e.g. "80/tcp".
	//
	// +required
	Name string `json:"name"`
	// Bindings to the host port; can contain multiple entries to e.g. express
	// IPv4 and IPv6 bindings.
	//
	// +required
	Bindings []ContainerPortBinding `json:"bindings"`
}

// ContainerSpec defines the configuration the container was created with.
type ContainerSpec struct {
	// State is the desired state of the container.
	//
	// +required
	// +kubebuilder:default:=running
	// +kubebuilder:validation:Enum=created;running
	State ContainerStatusValue `json:"state"`
}

// ContainerStatus defines the observed state of the container.
type ContainerStatus struct {
	// Name of the container; this is distinct from the container ID.
	//
	// +required
	Name string `json:"name"`
	// Namespace is the container namespace; refers to a `ContainerNamespace`
	// object in the same Kubernetes namespace.
	//
	// +required
	Namespace string `json:"namespace"`
	// Path to the executable (within the image) for the process.
	//
	// +required
	Path string `json:"path"`
	// Args is the arguments to the executable.
	//
	// +optional
	Args []string `json:"args"`
	// Image is the image the container was created with.
	//
	// +required
	Image string `json:"image"`
	// Ports describes the exposed ports of the container.
	//
	// +optional
	Ports []ContainerPort `json:"ports,omitempty"`
	// Labels are the container labels.
	//
	// +optional
	Labels map[string]string `json:"labels"`
	// Status of the container.
	//
	// +required
	// +kubebuilder:default:=unknown
	Status ContainerStatusValue `json:"status"`
	// Pid is the process identifier for the main process in the container.
	//
	// +optional
	Pid int32 `json:"pid"`
	// ExitCode is the exit status of the main process in the container.
	//
	// +optional
	ExitCode int32 `json:"exitCode"`
	// Error message if the container has failed to start.
	//
	// +optional
	Error string `json:"error"`
	// CreatedAt is the time this container was initially created.
	//
	// +optional
	CreatedAt metav1.Time `json:"createdAt"`
	// StartedAt is the time this container was started; unset if the container is stopped.
	//
	// +optional
	StartedAt metav1.Time `json:"startedAt"`
	// FinishedAt is the time this container was last stopped; unset if the container never ran.
	//
	// +optional
	FinishedAt metav1.Time `json:"finishedAt"`
	// Conditions represent the calculated state of the container.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Known condition types include:
	// - "Running": the container is running; this may also be paused.
	// - "Paused": the container is paused.
	// - "Restarting": the container is restarting.
	// - "OOMKilled": a process within this container has been killed because it ran out of memory
	//                since the container was last started.
	// - "Dead": the container is dead.
	//
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:categories="all"
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.status.namespace
// +kubebuilder:printcolumn:name="Running",type=boolean,JSONPath=`.status.conditions[?(@.type=="Running")].status`

// Container is the Schema for the containers API.
type Container struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of Container
	//
	// +required
	Spec ContainerSpec `json:"spec"`

	// Status defines the observed state of Container
	//
	// +optional
	Status ContainerStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ContainerList contains a list of Container.
type ContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Container `json:"items"`
}

// ContainerCreateRequestSpec defines the desired state for creating a container.
type ContainerCreateRequestSpec struct {
	// Name of the container to create; if not specified, a random name will be
	// generated.
	//
	// +optional
	Name string `json:"name"`
	// Namespace is the container namespace; refers to a `ContainerNamespace`
	// object in the same Kubernetes namespace.  If not specified, the container
	// will be created in the default namespace.
	//
	// +optional
	Namespace string `json:"namespace"`
	// Path is the path to the executable (within the image) for the process.
	// Defaults to the image's default command if not specified.
	//
	// +optional
	Path string `json:"path"`
	// Args is the arguments to the executable.
	//
	// +optional
	Args []string `json:"args"`
	// Image is the image the container was created with.
	// May be either a tag or a digest such as `sha256:dead00beef...`.
	//
	// +required
	Image string `json:"image"`
	// Ports describes the exposed ports of the container.
	//
	// +optional
	Ports []ContainerPort `json:"ports,omitempty"`
	// Labels are the container labels.  They are merged with the image labels.
	//
	// +optional
	Labels map[string]string `json:"labels"`

	// State is the desired state of the container.
	//
	// +required
	// +kubebuilder:default:=running
	// +kubebuilder:validation:Enum=created;running
	State ContainerStatusValue `json:"state"`
}

// ContainerCreateRequestStatus defines the status for a container creation request.
type ContainerCreateRequestStatus struct {
	// Name is the name of the created container; this is the container ID.
	//
	// +required
	Name string `json:"name"`

	// Conditions represent the state of the container creation request.
	// Current known condition types include:
	// - "Complete": the container creation request has successfully completed.
	// - "Failed": the container creation request has failed.
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.spec.namespace
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=container

// ContainerCreateRequest defines a request to create a new container.
// After a container has been created, the ContainerCreateRequest object will
// be deleted after a short delay.
type ContainerCreateRequest struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of Container
	//
	// +required
	Spec ContainerCreateRequestSpec `json:"spec"`

	// Status represents the current state of the ContainerCreateRequest
	//
	// +optional
	Status ContainerCreateRequestStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ContainerCreateRequestList contains a list of ContainerCreateRequest.
type ContainerCreateRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ContainerCreateRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&Container{}, &ContainerList{},
		&ContainerCreateRequest{}, &ContainerCreateRequestList{},
	)
}
