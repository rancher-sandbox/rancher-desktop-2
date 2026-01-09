// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageStatus defines the observed state of the image.
type ImageStatus struct {
	// id is the image ID, as reported by the container runtime.
	// +required
	ID string `json:"id"`
	// repoTag is the tag of the image.  Images with multiple tags will have
	// multiple Image objects.  Images without tags will have this unset, but
	// only one Image object should exist in that case.
	// +optional
	RepoTag string `json:"repoTag"`
	// repoDigests are the signed digests of the image.
	// +optional
	RepoDigests []string `json:"repoDigests,omitempty"`
	// createdAt is the time the volume was created.
	// +optional
	CreatedAt metav1.Time `json:"createdAt"`
	// architecture associated with the image.
	// +required
	Architecture string `json:"architecture"`
	// os associated with the image.
	// +required
	OS string `json:"os"`
	// size of the image.
	// +required
	Size int64 `json:"size"`
	// Labels of the image.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// conditions represent the state of the image.
	// There are currently no defined condition types.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.status.repoTag
// +kubebuilder:printcolumn:name="Tag",type=string,JSONPath=`.status.repoTag`
// +kubebuilder:printcolumn:name="Created",type=date,JSONPath=`.status.createdAt`
// +kubebuilder:printcolumn:name="Size",type=integer,format=byte,JSONPath=`.status.size`

// Image is the Schema for the images API.
type Image struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// status defines the immutable properties of an Image.
	// +optional
	Status ImageStatus `json:"status"`
}

// +kubebuilder:object:root=true

// ImageList contains a list of Image.
type ImageList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Image `json:"items"`
}

type ImagePullRequestSpec struct {
	// RepoTag is the image to pull.
	// +required
	RepoTag string `json:"repoTag"`
}

type ImagePullRequestStatus struct {
	// conditions represent the state of the image pull request.
	// Current known condition types include:
	//  - "Pulled": the image pull request has been completed.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=image

// ImagePullRequest defines a request to pull a new image.
// After an image has been pulled, the ImagePullRequest object will
// be deleted after a short delay.
type ImagePullRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ImagePullRequest
	// +required
	Spec ImagePullRequestSpec `json:"spec"`

	// status represents the current state of the ImagePullRequest
	// +optional
	Status ImagePullRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImagePullRequestList contains a list of ImagePullRequest.
type ImagePullRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImagePullRequest `json:"items"`
}

type ImagePushRequestSpec struct {
	// imageRef is the image to push.
	// This must be the name of an Image object in the same namespace.
	// +required
	ImageRef string `json:"imageRef"`
}

type ImagePushRequestStatus struct {
	// conditions represent the state of the image push request.
	// Current known condition types include:
	//  - "Finished": the image push request has been completed.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=image

// ImagePushRequest defines a request to push an image.
type ImagePushRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ImagePushRequest
	// +required
	Spec ImagePushRequestSpec `json:"spec"`

	// status represents the current state of the ImagePushRequest
	// +optional
	Status ImagePushRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImagePushRequestList contains a list of ImagePushRequest.
type ImagePushRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImagePushRequest `json:"items"`
}

// ImageScanRequestSpec defines the parameters for am image scan request.
type ImageScanRequestSpec struct {
	// imageRef is the image to scan.
	// This must be the name of an Image object in the same namespace.
	// +required
	ImageRef string `json:"imageRef"`
}

type ImageScanRequestStatus struct {
	// conditions represent the state of the image scan request.
	// Current known condition types include:
	//  - "Finished": the image scan request has been completed.
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// result is the result of the image scan.  This is the serialized JSON
	// output from the scanner.
	// +optional
	Result string `json:"result,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=image

// ImageScanRequest defines a request to scan an image.
type ImageScanRequest struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of ImageScanRequest
	// +required
	Spec ImageScanRequestSpec `json:"spec"`

	// status represents the current state of the ImageScanRequest
	// +optional
	Status ImageScanRequestStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ImageScanRequestList contains a list of ImageScanRequest.
type ImageScanRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ImageScanRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(
		&Image{}, &ImageList{},
		&ImagePullRequest{}, &ImagePullRequestList{},
		&ImageScanRequest{}, &ImageScanRequestList{},
	)
}
