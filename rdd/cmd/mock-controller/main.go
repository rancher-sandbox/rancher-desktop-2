// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command mock-controller automatically creates mock data on controller startup
// with static values.
package main

import (
	"os"

	// Import app controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/mock"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
)

func main() {
	os.Exit(external.RunControllers("mock"))
}
