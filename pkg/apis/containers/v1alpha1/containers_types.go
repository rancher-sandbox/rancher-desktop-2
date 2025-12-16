// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ContainerStatusValue describes the status of a container.
// +kubebuilder:validation:Enum=created;running;pausing;paused;restarting;removing;exited;dead;unknown
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
	// hostIp is the host IP address that the container's port is mapped to.
	// +required
	HostIP string `json:"hostIP"`
	// hostPort is the host port number that the container's port is mapped to.
	// +required
	HostPort string `json:"hostPort"`
}

// ContainerPort defines a single exposed port in a container.
type ContainerPort struct {
	// name of the port; in the form [port]/[protocol], e.g. "80/tcp".
	// +required
	Name string `json:"name"`
	// bindings to the host port; can contain multiple entries to e.g. express
	// IPv4 and IPv6 bindings.
	// +required
	Bindings []ContainerPortBinding `json:"bindings"`
}

// ContainerSpec defines the configuration the container was created with.
type ContainerSpec struct {
	// path is the path to the executable (within the image) for the process.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="path is immutable"
	Path string `json:"path"`
	// args is the arguments to the executable.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="args is immutable"
	Args []string `json:"args"`
	// image is the image the container was created with.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="image is immutable"
	Image string `json:"image"`
	// ports describes the exposed ports of the container.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ports is immutable"
	Ports []ContainerPort `json:"ports,omitempty"`
	// labels are the container labels.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="labels is immutable"
	Labels map[string]string `json:"labels"`
}

// ContainerStatus defines the observed state of the container.
type ContainerStatus struct {
	// status of the container.
	// +required
	// +kubebuilder:default:=unknown
	Status ContainerStatusValue `json:"status"`
	// pid is the process identifier for the main process in the container.
	// +required
	Pid int32 `json:"pid"`
	// exitCode is the exit status of the main process in the container.
	// +optional
	ExitCode int32 `json:"exitCode"`
	// error message if the container has failed to start.
	// +optional
	Error string `json:"error"`
	// createdAt is the time this container was initially created.
	// +optional
	CreatedAt metav1.Time `json:"createdAt"`
	// startedAt is the time this container was started; unset if the container is stopped.
	// +optional
	StartedAt metav1.Time `json:"startedAt"`
	// finishedAt is the time this container was last stopped; unset if the container never ran.
	// +optional
	FinishedAt metav1.Time `json:"finishedAt"`
	// conditions represent the calculated state of the container.
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
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Running",type=boolean,JSONPath=`.spec.running`

// Container is the Schema for the containers API.
type Container struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Container
	// +required
	Spec ContainerSpec `json:"spec"`

	// status defines the observed state of Container
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

func init() {
	SchemeBuilder.Register(&Container{}, &ContainerList{})
}
