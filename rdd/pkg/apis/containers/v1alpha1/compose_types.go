// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ComposeProjectKind is the Kind string for ComposeProject resources.
const ComposeProjectKind = "ComposeProject"

// ComposeUpRequestKind is the Kind string for ComposeUpRequest resources.
const ComposeUpRequestKind = "ComposeUpRequest"

// ComposeProject status conditions.
const (
	ComposeProjectConditionHasMembers = "HasMembers"
)

// ComposeProject status reasons for the HasMembers condition.
const (
	// ComposeHasMembersReasonFound indicates that the compose project has at
	// least one member.
	ComposeHasMembersReasonFound = "Found"
	// ComposeHasMembersReasonDeleted indicates that the last member of the
	// compose project has been removed; the project may be deleted soon.
	ComposeHasMembersReasonDeleted = "Deleted"
	// ComposeHasMembersReasonCalculating indicates that the controller is
	// still calculating whether the compose project has any members.
	ComposeHasMembersReasonCalculating = "Calculating"
)

// ComposeUpRequest status conditions.
const (
	// ComposeUpRequestConditionSettled indicates that `docker compose up` has
	// reached a terminal state (succeeded or failed); see the Reason for
	// which.
	ComposeUpRequestConditionSettled = "Settled"
	// ComposeUpRequestConditionFailed indicates that `docker compose up`
	// failed.
	ComposeUpRequestConditionFailed = "Failed"
)

// ComposeUpRequest status reason for the Settled condition.
const (
	// ComposeUpRequestSettledReasonSucceeded indicates that `docker compose
	// up` succeeded; this object will be reaped.
	ComposeUpRequestSettledReasonSucceeded = "Succeeded"
	// ComposeUpRequestSettledReasonFailed indicates that `docker compose up`
	// failed; this object will be reaped.
	ComposeUpRequestSettledReasonFailed = "Failed"
	// ComposeUpRequestSettledReasonRunning indicates that `docker compose up`
	// is still running; this object will be reaped once it completes.
	ComposeUpRequestSettledReasonRunning = "Running"
)

// ComposeUpRequest status reason for the Failed condition.
const (
	// ComposeUpRequestFailedReasonFailed indicates that `docker compose up`
	// failed; this object will be reaped.
	ComposeUpRequestFailedReasonFailed = "Failed"
	// We intentionally do not include a reason when succeeding, as the condition
	// should be removed in that case, per the design doc.
)

// ComposeProjectContainer describes a single container that is part of a compose
// project.
type ComposeProjectContainer struct {
	// Name is the metadata.name of the container object (i.e. the container
	// ID).
	//
	// +required
	Name string `json:"name"`
	// UID is the UID of the container object.
	//
	// +required
	UID types.UID `json:"uid"`
}

// ComposeProjectStatus defines the observed state of a `docker compose` project.
type ComposeProjectStatus struct {
	// Namespace is the container namespace; refers to a [ContainerNamespace]
	// object in the same Kubernetes namespace.
	//
	// +required
	Namespace string `json:"namespace"`
	// Name is the compose project name.
	//
	// +required
	Name string `json:"name"`
	// WorkingDir is the absolute path to the compose project directory on the
	// host (i.e. relative to where the RDD process runs). May be unset.
	//
	// +optional
	WorkingDir string `json:"workingDir,omitempty"`
	// Configs is the list of compose files used to create the project,
	// relative to WorkingDir. It is not guaranteed that this is sufficient to
	// recreate the project. May be unset.
	//
	// +optional
	Configs []string `json:"configs,omitempty"`
	// Containers tracks the containers that are part of the compose project.
	// The name is the metadata.name of the container object (i.e. the
	// container ID). The UID is also tracked.
	//
	// +listType=map
	// +listMapKey=name
	// +optional
	Containers []ComposeProjectContainer `json:"containers,omitempty"`
	// Conditions represent the state of the compose project.
	// Known condition types include:
	//
	// - "HasMembers": The compose project has at least one member.
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
// +kubebuilder:selectablefield:JSONPath=.status.name
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.status.name`
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=compose

// ComposeProject is the Schema for the compose API.  ComposeProject objects do
// not reflect actual container engine objects; instead, they reflect
// `docker compose` projects.
//
// ComposeProject objects should not be created directly by users: the
// reconciler creates (and keeps up to date) a ComposeProject object
// automatically upon noticing compose project references in containers (via the
// normal compose labels on them).  Said containers may have been created as a
// response to [ComposeUpRequest] objects.  metadata.name is constructed by
// joining `status.namespace` and `status.name` with a dot if the result is a
// valid Kubernetes name; otherwise it is `cmp-` followed by the lower-case
// SHA256 hash of that joined string.
type ComposeProject struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Status defines the observed state of ComposeProject
	//
	// +optional
	Status ComposeProjectStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ComposeProjectList contains a list of ComposeProject.
type ComposeProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComposeProject `json:"items"`
}

// ComposeUpRequestSpec defines the identity of the `docker compose` project
// to bring up.
type ComposeUpRequestSpec struct {
	// Namespace is the container namespace; refers to a [ContainerNamespace]
	// object in the same Kubernetes namespace.  Immutable once created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.namespace is immutable"
	Namespace string `json:"namespace"`
	// Name is the compose project name.  Immutable once created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.name is immutable"
	Name string `json:"name"`
	// WorkingDir is the absolute path to the compose project directory on the
	// host (i.e. where the RDD process runs).  Used to look up any files needed.
	//
	// +required
	WorkingDir string `json:"workingDir"`
	// Configs is the list of compose files used to create the project,
	// relative to workingDir.  It must be a descendent of workingDir.
	//
	// +optional
	Configs []string `json:"configs,omitempty"`
}

// ComposeUpRequestStatus defines the observed state of a ComposeUpRequest.
type ComposeUpRequestStatus struct {
	// Conditions represent the state of the request.
	// Known condition types include:
	//
	// - "Settled": `docker compose up` has reached a terminal state (see the
	//   Reason field for whether it succeeded or failed).
	// - "Failed": `docker compose up` failed.
	//
	// Failed can only be True if Settled is also True.  The status of each
	// condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.spec.namespace
// +kubebuilder:selectablefield:JSONPath=.spec.name
// +kubebuilder:metadata:annotations=rdd.rancherdesktop.io/controller=compose

// ComposeUpRequest is the Schema for the composeuprequests API.  Creating a
// ComposeUpRequest triggers `docker compose up` for the named project, and
// causes the corresponding [ComposeProject] object to be created (or updated)
// to reflect this project.  metadata.name must be constructed the same way as
// for a ComposeProject object, based on spec.namespace and spec.name; this is
// also the name of the resulting ComposeProject object.
//
// Once the underlying command has settled (successfully or not), this object is
// expected to be deleted after a short delay.
type ComposeUpRequest struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	//
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// Spec defines the identity of the compose project to bring up.
	//
	// +required
	Spec ComposeUpRequestSpec `json:"spec"`

	// Status defines the observed state of ComposeUpRequest
	//
	// +optional
	Status ComposeUpRequestStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ComposeUpRequestList contains a list of ComposeUpRequest.
type ComposeUpRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ComposeUpRequest `json:"items"`
}

func init() {
	registerTypes(
		&ComposeProject{}, &ComposeProjectList{},
		&ComposeUpRequest{}, &ComposeUpRequestList{},
	)
}
