// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package containernamespace registers the ContainerNamespace controller.
// ContainerNamespaces reflect container engine namespaces (e.g. containerd
// namespaces) as Kubernetes resources; deletion is guarded by a validating
// webhook to prevent orphaning owned containers.
package containernamespace
