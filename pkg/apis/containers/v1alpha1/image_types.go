// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageSpec defines the configuration the image was created with.
type ImageSpec struct {
	// repoTags are the known tags for the image.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repoTags is immutable"
	RepoTags []string `json:"repoTags,omitempty"`
	// repoDigests are the signed digests of the image.
	// + optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="repoDigests is immutable"
	RepoDigests []string `json:"repoDigests,omitempty"`
	// createdAt is the time the volume was created.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="createdAt is immutable"
	CreatedAt metav1.Time `json:"createdAt"`
	// architecture associated with the image.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="architecture is immutable"
	Architecture string `json:"architecture"`
	// os associated with the image.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="os is immutable"
	OS string `json:"os"`
	// size of the image.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="size is immutable"
	Size int64 `json:"size"`
	// Labels of the image.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="labels is immutable"
	Labels map[string]string `json:"labels,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Driver",type=string,JSONPath=`.spec.driver`

// Image is the Schema for the images API.
type Image struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Image
	// +required
	Spec ImageSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ImageList contains a list of Image.
type ImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Image `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Image{}, &ImageList{})
}
