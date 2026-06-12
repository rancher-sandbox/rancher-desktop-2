// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ImageStatus defines the observed state of the image.
type ImageStatus struct {
	// Namespace is the container namespace; refers to a `ContainerNamespace`
	// object in the same Kubernetes namespace.
	//
	// +required
	Namespace string `json:"namespace"`
	// ID is the image ID, as reported by the container runtime.
	//
	// +required
	ID string `json:"id"`
	// RepoTag is the tag of the image.  Images with multiple tags will have
	// multiple Image objects.  Images without tags will have this unset, but
	// only one Image object should exist in that case.
	//
	// +optional
	RepoTag string `json:"repoTag"`
	// RepoDigests are the signed digests of the image.
	//
	// +optional
	RepoDigests []string `json:"repoDigests,omitempty"`
	// CreatedAt is the time the image was created.
	//
	// +optional
	CreatedAt metav1.Time `json:"createdAt"`
	// Architecture associated with the image.
	//
	// +required
	Architecture string `json:"architecture"`
	// OS associated with the image.
	//
	// +required
	OS string `json:"os"`
	// Size of the image.
	//
	// +required
	Size int64 `json:"size"`
	// Labels of the image.
	//
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Conditions represent the state of the image.
	// There are currently no defined condition types.
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
// +kubebuilder:selectablefield:JSONPath=.status.id
// +kubebuilder:selectablefield:JSONPath=.status.repoTag
// +kubebuilder:printcolumn:name="Tag",type=string,JSONPath=`.status.repoTag`
// +kubebuilder:printcolumn:name="Created",type=date,JSONPath=`.status.createdAt`
// +kubebuilder:printcolumn:name="Size",type=integer,format=byte,JSONPath=`.status.size`

// Image is the Schema for the images API.
type Image struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Status defines the immutable properties of an Image.
	//
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

// ImagePullRequestSpec defines the parameters for pulling an image.
type ImagePullRequestSpec struct {
	// Namespace is the container namespace; refers to a `ContainerNamespace`
	// object in the same Kubernetes namespace.  If not specified, the image
	// will be pulled into the default namespace.
	//
	// +optional
	Namespace string `json:"namespace"`
	// RepoTag is the image to pull.
	//
	// +required
	RepoTag string `json:"repoTag"`
}

// ImagePullRequestStatus reports the progress of an image pull request.
type ImagePullRequestStatus struct {
	// Conditions represent the state of the image pull request.
	// Current known condition types include:
	//  - "Complete": the image pull request has successfully completed.
	//  - "Failed": the image pull request has failed.
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.spec.namespace
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=image

// ImagePullRequest defines a request to pull a new image.
// After an image has been pulled, the ImagePullRequest object will
// be deleted after a short delay.
type ImagePullRequest struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of ImagePullRequest
	//
	// +required
	Spec ImagePullRequestSpec `json:"spec"`

	// Status represents the current state of the ImagePullRequest
	//
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

// ImagePushRequestSpec defines the parameters for pushing an image.
type ImagePushRequestSpec struct {
	// ImageRef is the image to push.
	// This must be the name of an Image object in the same Kubernetes namespace.
	//
	// +required
	ImageRef string `json:"imageRef"`
}

// ImagePushRequestStatus reports the progress of an image push request.
type ImagePushRequestStatus struct {
	// Conditions represent the state of the image push request.
	// Current known condition types include:
	//  - "Complete": the image push request has successfully completed.
	//  - "Failed": the image push request has failed.
	// The status of each condition is one of True, False, or Unknown.
	//
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

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of ImagePushRequest
	//
	// +required
	Spec ImagePushRequestSpec `json:"spec"`

	// Status represents the current state of the ImagePushRequest
	//
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

// ImageScanRequestSpec defines the parameters for an image scan request.
type ImageScanRequestSpec struct {
	// ImageRef is the image to scan.
	// This must be the name of an Image object in the same namespace.
	//
	// +required
	ImageRef string `json:"imageRef"`
}

// ImageScanRequestStatus reports the progress and result of an image scan request.
type ImageScanRequestStatus struct {
	// Conditions represent the state of the image scan request.
	// Current known condition types include:
	//  - "Complete": the image scan request has successfully completed.
	//  - "Failed": the image scan request has failed.
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Result is the result of the image scan.  This is the serialized JSON
	// output from the scanner.
	//
	// +optional
	Result string `json:"result,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=image

// ImageScanRequest defines a request to scan an image.
type ImageScanRequest struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the desired state of ImageScanRequest
	//
	// +required
	Spec ImageScanRequestSpec `json:"spec"`

	// Status represents the current state of the ImageScanRequest
	//
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
	registerTypes(
		&Image{}, &ImageList{},
		&ImagePullRequest{}, &ImagePullRequestList{},
		&ImagePushRequest{}, &ImagePushRequestList{},
		&ImageScanRequest{}, &ImageScanRequestList{},
	)
}
