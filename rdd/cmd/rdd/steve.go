// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// The app runs steve only to serve the dashboard, so builds without the
// dashboard leave out this command and pkg/steve.

//go:build with_dashboard

package main

import (
	"github.com/spf13/cobra"

	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/steve"
)

func init() {
	optionalCommands = append(optionalCommands, newSteveCommand)
}

func newSteveCommand() *cobra.Command {
	var opts steve.Options
	cmd := &cobra.Command{
		Use:    "steve",
		Short:  "Serve the steve API and the Cluster Explorer dashboard",
		Args:   cobra.NoArgs,
		Hidden: !developer.Mode(),
		RunE: func(*cobra.Command, []string) error {
			return steve.Run(genericapiserver.SetupSignalContext(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().StringVar(&opts.Context, "context", "", "Kubeconfig context to use")
	return cmd
}
