// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VolumeSpec defines the configuration the volume was created with.
type VolumeSpec struct {
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
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver`

// Volume is the Schema for the volumes API.
type Volume struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Volume
	// +required
	Spec VolumeSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// VolumeList contains a list of Volume.
type VolumeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Volume `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Volume{}, &VolumeList{})
}
