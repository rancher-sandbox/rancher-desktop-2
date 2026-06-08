// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows && !external_wsl2

package limavm

// Import wsl2 driver to register it in the registry on Windows.
import _ "github.com/lima-vm/lima/v2/pkg/driver/wsl2"
