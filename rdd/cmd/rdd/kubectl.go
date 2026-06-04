// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/component-base/cli"
	kubectlcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/util"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/kuberlr"
)

func newCtlCommand() *cobra.Command {
	command := &cobra.Command{
		Use:                "ctl",
		Short:              "Run the kubectl command against the RDD control plane",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               ctlAction,
	}
	return command
}

func ctlAction(cmd *cobra.Command, args []string) error {
	if err := ensureServiceRunning(cmd.Context()); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "wait-condition" {
		return ctlWaitConditionAction(cmd, args[1:])
	}
	if err := os.Setenv("KUBECONFIG", instance.Config()); err != nil {
		return fmt.Errorf("failed to set KUBECONFIG: %w", err)
	}
	// Always targets the embedded apiserver, version-matched to the
	// embedded kubectl by construction (see SkipResolver).
	kuberlr.SkipResolver()
	return kubectlAction(cmd, args)
}

func newKubectlCommand() *cobra.Command {
	command := &cobra.Command{
		Use:                "kubectl",
		Short:              "Run the kubectl command",
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               kubectlAction,
	}
	return command
}

func kubectlAction(cmd *cobra.Command, args []string) error {
	path, err := kuberlr.Resolve(cmd.Context(), args)
	if err != nil {
		// Falling back here would hide a download/sha-mismatch error
		// behind confusing kubectl errors; probe failures return "" from
		// Resolve instead and never reach this branch.
		return fmt.Errorf("resolving kubectl version: %w", err)
	}
	if path != "" {
		logrus.WithField("path", path).Debug("using cached kubectl")
		return kuberlr.Exec(cmd.Context(), path, args)
	}
	os.Args = os.Args[1:]
	command := kubectlcmd.NewDefaultKubectlCommand()
	if err := cli.RunNoErrOutput(command); err != nil {
		util.CheckErr(err)
	}
	return nil
}
