// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package v1alpha1 contains API Schema definitions for the app v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=app.rancherdesktop.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "app.rancherdesktop.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &runtime.SchemeBuilder{}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// registerTypes adds the given objects to the SchemeBuilder under GroupVersion,
// along with the group-version's metadata types.
func registerTypes(objects ...runtime.Object) {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(GroupVersion, objects...)
		metav1.AddToGroupVersion(scheme, GroupVersion)
		return nil
	})
}
