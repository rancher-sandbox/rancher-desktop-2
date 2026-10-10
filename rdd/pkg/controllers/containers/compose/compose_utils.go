// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

// mirrorFinalizer is added to mirror resources so user deletions
// are forwarded to the container engine before the resource is removed.
const mirrorFinalizer = "engine.rancherdesktop.io/mirror"
