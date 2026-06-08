// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build darwin && !external_vz && !no_vz

package limavm

// Import vz driver to register it in the registry on darwin.
import _ "github.com/lima-vm/lima/v2/pkg/driver/vz"
