// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"runtime"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/nerdctlstub"
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

All arguments pass through to nerdctl; on Windows, arguments referring to
host paths are rewritten to the /mnt/<drive> locations where the guest
mounts them.`,
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		SilenceErrors:      true,
		RunE:               nerdctlAction,
	}
}

func nerdctlAction(cmd *cobra.Command, args []string) error {
	guestArgs := args
	if runtime.GOOS == "windows" {
		parsed, err := nerdctlstub.TranslateCommandLine(args)
		if err != nil {
			return err
		}
		defer func() {
			if err := parsed.RunCleanups(); err != nil {
				logrus.WithError(err).Warn("failed to clean up translated arguments")
			}
		}()
		guestArgs = parsed.Args
	}
	if err := ensureAppRunning(cmd.Context(), "nerdctl"); err != nil {
		return err
	}
	// nerdctl must run as root in the guest: non-root nerdctl insists on
	// rootless mode even when given an explicit --address.
	// TODO: let the distro's nerdctl wrapper own the address instead.
	guestCmd := append([]string{"sudo", "nerdctl", "--address", containerdGuestSocket}, guestArgs...)
	return limaVMGuestExec(cmd.Context(), appLimaVMName, "", "", guestCmd)
}
