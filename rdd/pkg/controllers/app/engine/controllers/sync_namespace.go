// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// syncContainerNamespace creates the "moby" ContainerNamespace mirror.
// This resource has no mirror finalizer: Docker has no corresponding
// engine object for the reverse delete, and cleanupMirrorResources
// sweeps it unconditionally on VM stop, so a finalizer with no
// handler would only trap user deletes in Terminating.
func (w *dockerWatcher) syncContainerNamespace(ctx context.Context) error {
	applyConfig := containersv1alpha1apply.ContainerNamespace(containerNamespace, w.apiNamespace)

	return w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
}
