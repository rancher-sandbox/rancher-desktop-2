// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command app-controller runs the app controller as a standalone process, for
// development and testing outside the embedded control plane.
package main

import (
	"os"

	// Import app controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/app"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/demo"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	os.Exit(external.RunControllers("app"))
}
