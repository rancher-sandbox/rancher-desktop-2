// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command extensions-controller runs the extensions controller as a standalone
// process, for development and testing outside the embedded control plane.
package main

import (
	"os"

	// Import extensions controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/extensions/extension"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	os.Exit(external.RunControllers("extensions"))
}
