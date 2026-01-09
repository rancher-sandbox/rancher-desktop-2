// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VolumeStatus describes the configuration the volume was created with.
type VolumeStatus struct {
	// createdAt is the time the volume was created.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="createdAt is immutable"
	CreatedAt metav1.Time `json:"createdAt"`
	// driver the volume uses.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="driver is immutable"
	Driver string `json:"driver"`
	// mountpoint is where on the host the volume is mounted.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="mountpoint is immutable"
	MountPoint string `json:"mountpoint"`
	// labels for the volume.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="labels is immutable"
	Labels map[string]string `json:"labels,omitempty"`
	// scope of the volume.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="scope is immutable"
	Scope string `json:"scope"`
	// options for the volume driver.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="options is immutable"
	Options map[string]string `json:"options,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.status.driver`

// Volume is the Schema for the volumes API.
type Volume struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// status describes the observed state of the volume.
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
	// driver the volume should use.
	// +required
	Driver string `json:"driver"`
}

type VolumeCreateStatus struct {
	// conditions represent the state of the volume creation request.
	// Current known condition types include:
	// - "Processed": the volume creation request has been completed.
	// The status of each condition is one of True, False, or Unknown.
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

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of VolumeCreateRequest
	// +required
	Spec VolumeCreateSpec `json:"spec"`

	// status represents the current state of the VolumeCreateRequest
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
