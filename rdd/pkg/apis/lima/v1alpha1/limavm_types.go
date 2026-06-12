// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LimaVMKind is the Kind string for LimaVM resources.
const LimaVMKind = "LimaVM"

// TemplateConfigMapKey is the key used to store the template text in the templateConfigMap.
const TemplateConfigMapKey = "template"

// AnnotationRestartRequested triggers a restart when set on a LimaVM resource.
// The reconciler translates it to status.restartNeeded and removes the annotation.
var AnnotationRestartRequested = SchemeGroupVersion.Group + "/restartRequested"

// TemplateReference specifies a reference to a ConfigMap containing a VM template.
type TemplateReference struct {
	// name is the name of the ConfigMap containing the VM template.
	// The ConfigMap must have a "template" key.
	// +required
	Name string `json:"name"`

	// namespace is the namespace of the ConfigMap.
	// If not specified, defaults to the namespace of the LimaVM resource.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// LimaVMSpec defines the desired state of LimaVM.
type LimaVMSpec struct {
	// templateRef is a reference to a ConfigMap containing the VM template.
	// This field is immutable after creation.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="templateRef is immutable"
	TemplateRef TemplateReference `json:"templateRef"`

	// running specifies whether the VM should be running
	// +kubebuilder:default=false
	Running bool `json:"running"`
}

// LimaVMStatus defines the observed state of LimaVM.
type LimaVMStatus struct {
	// conditions represent the current state of the LimaVM resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// templateConfigMap is the name of the ConfigMap containing the validated template.
	// This ConfigMap is created and managed by the controller and is owned by this LimaVM resource.
	// An admission controller validates any updates to the ConfigMap.
	// This field is informational - internal code should use GetTemplateConfigMapName() instead.
	// +optional
	TemplateConfigMap string `json:"templateConfigMap,omitempty"`

	// observedTemplateResourceVersion tracks the resourceVersion of the template
	// ConfigMap last applied to the instance. For stopped instances, this is
	// updated after writing lima.yaml to disk. For running instances, it is
	// deferred until the restart completes.
	// When this differs from the ConfigMap's current resourceVersion, the
	// reconciler checks for template changes.
	// +optional
	ObservedTemplateResourceVersion string `json:"observedTemplateResourceVersion,omitempty"`

	// restartNeeded indicates a restart has been requested but not yet executed.
	// Set by the reconciler when it processes a restartRequested annotation or
	// detects a template change on a running instance. Cleared when the instance
	// stops (before the restart starts) or immediately if already stopped.
	// +optional
	RestartNeeded bool `json:"restartNeeded,omitempty"`

	// restartCount tracks how many times the instance has reached the Running state.
	// Incremented each time the controller sets Running=True/Started.
	// +optional
	RestartCount int32 `json:"restartCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:categories="all"
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Running",type=boolean,JSONPath=`.spec.running`

// LimaVM is the Schema for the limavms API.
type LimaVM struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of LimaVM
	// +required
	Spec LimaVMSpec `json:"spec"`

	// status defines the observed state of LimaVM
	// +optional
	Status LimaVMStatus `json:"status,omitempty,omitzero"`
}

// GetTemplateConfigMapName returns the name of the template ConfigMap for this LimaVM.
// This is the single source of truth for the naming convention.
func (vm *LimaVM) GetTemplateConfigMapName() string {
	return vm.Name + "-template"
}

// +kubebuilder:object:root=true

// LimaVMList contains a list of LimaVM.
type LimaVMList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LimaVM `json:"items"`
}

func init() {
	registerTypes(&LimaVM{}, &LimaVMList{})
}
