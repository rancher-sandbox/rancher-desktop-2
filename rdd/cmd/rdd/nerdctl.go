// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"github.com/spf13/cobra"
)

// appLimaVMName is the LimaVM instance the App controller manages.
const appLimaVMName = "rd"

// containerdGuestSocket is the shared containerd socket inside the VM,
// matching the guestSocket forward in the App controller's lima template.
const containerdGuestSocket = "/run/k3s/containerd/containerd.sock"

func newNerdctlCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "nerdctl",
		Short: "Run nerdctl inside the Rancher Desktop VM",
		Long: `Run nerdctl inside the Rancher Desktop VM.

All arguments pass through to nerdctl.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		RunE:               nerdctlAction,
	}
}

func nerdctlAction(cmd *cobra.Command, args []string) error {
	if err := ensureAppRunning(cmd.Context(), "nerdctl"); err != nil {
		return err
	}
	// nerdctl must run as root in the guest. Non-root nerdctl insists on
	// rootless mode even when given an explicit --address.
	// TODO: let the distro's nerdctl wrapper own the address instead.
	guestCmd := append([]string{"sudo", "nerdctl", "--address", containerdGuestSocket}, args...)
	return limaVMGuestExec(cmd.Context(), appLimaVMName, "", "", guestCmd)
}
