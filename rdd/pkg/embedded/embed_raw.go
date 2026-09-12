// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build with_distro && !windows

package embedded

import _ "embed"

//go:embed distro.raw.xz
var rawImage string

func init() {
	Distro = rawImage
}
