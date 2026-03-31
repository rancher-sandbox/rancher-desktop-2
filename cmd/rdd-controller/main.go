// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command rdd-controller runs the core rdd controllers as a standalone process,
// for development and testing outside the embedded control plane.
package main

import (
	"os"

	// Import rdd controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/configmapreplicaset"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/notary"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	os.Exit(external.RunControllers("rdd"))
}
