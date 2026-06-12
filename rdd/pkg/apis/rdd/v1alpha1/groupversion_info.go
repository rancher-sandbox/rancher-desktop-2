// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package v1alpha1 defines the rdd.rancherdesktop.io API group, which contains
// system-level resources: ConfigMapReplicaSet for declarative ConfigMap
// replication and Notary for tracking configuration changes.
// +kubebuilder:object:generate=true
// +kubebuilder:ac:generate=true
// +groupName=rdd.rancherdesktop.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// SchemeGroupVersion is group version used to register these objects.
	SchemeGroupVersion = schema.GroupVersion{Group: "rdd.rancherdesktop.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &runtime.SchemeBuilder{}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// registerTypes adds the given objects to the SchemeBuilder under
// SchemeGroupVersion, along with the group-version's metadata types.
func registerTypes(objects ...runtime.Object) {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion, objects...)
		metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
		return nil
	})
}
