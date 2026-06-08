// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build (darwin || linux) && !external_qemu

package limavm

// Import qemu driver to register it in the registry on macOS and Linux.
import _ "github.com/lima-vm/lima/v2/pkg/driver/qemu"
