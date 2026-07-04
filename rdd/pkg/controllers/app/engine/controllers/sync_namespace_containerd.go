// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	containersv1alpha1apply "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1/applyconfiguration/containers/v1alpha1"
)

// syncNamespaces lists containerd namespaces, creates or updates their
// ContainerNamespace mirrors, and prunes stale ones. Unlike Docker's static
// "moby" namespace, containerd namespaces come and go.
func (w *containerdWatcher) syncNamespaces(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("containerd-watcher")

	nsNames, err := w.cli.NamespaceService().List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	// Track which mirror names we create so stale mirrors can be pruned.
	activeNames := make(map[string]bool, len(nsNames))

	// Log and skip per-item Apply failures rather than failing the whole
	// startup: a single permanently-broken Apply must not pin
	// ContainerEngineReady at ConnectFailed. Structural errors below are
	// still fatal.
	var errs []error
	for _, ns := range nsNames {
		if len(validation.IsDNS1123Subdomain(ns)) == 0 {
			activeNames[ns] = true
		}
		if err := w.applyNamespace(ctx, ns); err != nil {
			log.Error(err, "Skipping namespace during full sync", "namespace", ns)
		}
	}

	// Remove stale ContainerNamespace mirrors.
	var nsMirrors containersv1alpha1.ContainerNamespaceList
	if err := w.k8s.List(ctx, &nsMirrors, client.InNamespace(w.apiNamespace)); err != nil {
		return fmt.Errorf("failed to list ContainerNamespaces: %w", err)
	}
	for i := range nsMirrors.Items {
		ns := &nsMirrors.Items[i]
		if !activeNames[ns.Name] {
			log.V(1).Info("Removing stale ContainerNamespace", "namespace", ns.Name)
			if err := w.removeMirrorResource(ctx, ns, ns.Name); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

// applyNamespace creates the ContainerNamespace mirror for a containerd
// namespace. This resource has no mirror finalizer: containerd has no
// corresponding engine object for the reverse delete, and
// cleanupMirrorResources sweeps it unconditionally on VM stop, so a
// finalizer with no handler would only trap user deletes in Terminating.
func (w *containerdWatcher) applyNamespace(ctx context.Context, ns string) error {
	// containerd namespace names may contain uppercase or underscores, which
	// are invalid in K8s object names. Skip the mirror; containers in such a
	// namespace still get mirrored (their mirror name hashes).
	if len(validation.IsDNS1123Subdomain(ns)) > 0 {
		logf.FromContext(ctx).WithName("containerd-watcher").
			V(1).Info("Skipping ContainerNamespace mirror for non-DNS1123 namespace", "namespace", ns)
		return nil
	}

	applyConfig := containersv1alpha1apply.ContainerNamespace(ns, w.apiNamespace)

	return w.k8s.Apply(ctx, applyConfig,
		client.ForceOwnership, client.FieldOwner(controllerName))
}
