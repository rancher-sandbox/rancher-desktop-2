// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HostInfoSpec is empty; HostInfo is a read-only singleton managed entirely
// by the controller.
type HostInfoSpec struct{}

// HostInfoStatus reports the detected host hardware limits. Both fields are
// serialized even when zero: a zero reading means detection failed, and the App
// webhook then leaves the matching ceiling unenforced, which a client cannot
// distinguish from an absent field.
type HostInfoStatus struct {
	// cpus is the number of logical CPUs on the host.
	// +optional
	CPUs int `json:"cpus"`
	// memory is the total host RAM, as a quantity so that clients can compare it
	// against spec.virtualMachine.memory without converting units.
	// +optional
	Memory resource.Quantity `json:"memory"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=hostinfos,categories="all"
// +kubebuilder:printcolumn:name="CPUs",type=integer,JSONPath=".status.cpus"
// +kubebuilder:printcolumn:name="Memory",type=string,JSONPath=".status.memory"

// HostInfo is a cluster-scoped singleton that exposes host hardware limits
// (CPU count and total memory) so that clients such as the GUI can determine
// valid ranges for VM resource settings without inspecting the host directly.
// The controller creates and maintains exactly one instance named "system".
type HostInfo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HostInfoSpec   `json:"spec,omitempty"`
	Status HostInfoStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HostInfoList contains a list of HostInfo.
type HostInfoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HostInfo `json:"items"`
}

func init() {
	registerTypes(&HostInfo{}, &HostInfoList{})
}
