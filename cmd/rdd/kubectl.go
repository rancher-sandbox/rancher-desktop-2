// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	// Import to initialize client auth plugins.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/component-base/cli"
	kubectlcmd "k8s.io/kubectl/pkg/cmd"
	"k8s.io/kubectl/pkg/cmd/util"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
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
	if !service.Exists() {
		if err := service.Create(nil); err != nil {
			return err
		}
	}
	if !service.Running() {
		if err := service.Start(cmd.Context(), nil); err != nil {
			return err
		}
	}
	if err := service.WaitWithTimeout(cmd.Context()); err != nil {
		return err
	}
	if err := os.Setenv("KUBECONFIG", instance.Config()); err != nil {
		return fmt.Errorf("failed to set KUBECONFIG: %w", err)
	}
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

func kubectlAction(*cobra.Command, []string) error {
	os.Args = os.Args[1:]
	command := kubectlcmd.NewDefaultKubectlCommand()
	if err := cli.RunNoErrOutput(command); err != nil {
		util.CheckErr(err)
	}
	return nil
}
