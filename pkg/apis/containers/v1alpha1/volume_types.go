// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VolumeStatus describes the configuration the volume was created with.
type VolumeStatus struct {
	// Name of the volume.
	//
	// +required
	Name string `json:"name"`
	// Namespace of the volume; refers to a `ContainerNamespace` object in the
	// same Kubernetes namespace.
	//
	// +required
	Namespace string `json:"namespace"`
	// CreatedAt is the time the volume was created.
	//
	// +required
	CreatedAt metav1.Time `json:"createdAt"`
	// Driver the volume uses.
	//
	// +required
	Driver string `json:"driver"`
	// MountPoint is where on the host the volume is mounted.
	//
	// +required
	MountPoint string `json:"mountpoint"`
	// Labels for the volume.
	//
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// Scope of the volume.
	//
	// +required
	Scope string `json:"scope"`
	// Options for the volume driver.
	//
	// +optional
	Options map[string]string `json:"options,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:categories="all"
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.status.namespace
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.status.driver`

// Volume is the Schema for the volumes API.
type Volume struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Status describes the observed state of the volume.
	//
	// +optional
	Status VolumeStatus `json:"status"`
}

// +kubebuilder:object:root=true

// VolumeList contains a list of Volume.
type VolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Volume `json:"items"`
}

type VolumeCreateSpec struct {
	// Name of the volume to create; if not specified, a random name will be
	// generated.
	//
	// +optional
	Name string `json:"name"`
	// Namespace of the volume; refers to a `ContainerNamespace` object in the
	// same Kubernetes namespace.  If not specified, the volume will be created
	// in the default namespace.
	//
	// +optional
	Namespace string `json:"namespace"`
	// Driver the volume should use.
	//
	// +required
	Driver string `json:"driver"`
}

type VolumeCreateStatus struct {
	// Conditions represent the state of the volume creation request.
	// Current known condition types include:
	//  - "Complete": the volume creation request has successfully completed.
	//  - "Failed": the volume creation request has failed.
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=volume

// VolumeCreateRequest defines a request to create a new volume.
// After a volume has been created, the VolumeCreateRequest object will
// be deleted after a short delay.
type VolumeCreateRequest struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of VolumeCreateRequest
	//
	// +required
	Spec VolumeCreateSpec `json:"spec"`

	// Status represents the current state of the VolumeCreateRequest
	//
	// +optional
	Status VolumeCreateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// VolumeCreateRequestList contains a list of VolumeCreateRequest.
type VolumeCreateRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []VolumeCreateRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&Volume{}, &VolumeList{},
		&VolumeCreateRequest{}, &VolumeCreateRequestList{},
	)
}
