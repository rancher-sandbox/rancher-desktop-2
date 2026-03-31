// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Command lima-controller runs the Lima VM controller as a standalone process,
// for development and testing outside the embedded control plane.
package main

import (
	"os"

	"github.com/spf13/cobra"

	// Import rdd controller packages to trigger init() functions.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/lima/limavm"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/external"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostagent"
)

func main() {
	// Check if first arg is "hostagent" to handle it via cobra
	// Otherwise use the existing external.RunControllers which handles its own flags
	if len(os.Args) > 1 && os.Args[1] == "hostagent" {
		cmd := &cobra.Command{
			Use:   "lima-controller",
			Short: "Lima controller for Rancher Desktop Daemon",
		}
		cmd.AddCommand(hostagent.NewCommand())
		if err := cmd.Execute(); err != nil {
			os.Exit(1)
		}
		return
	}

	// Default: run controllers
	os.Exit(external.RunControllers("lima"))
}
