// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"github.com/spf13/cobra"

	genericapiserver "k8s.io/apiserver/pkg/server"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/steve"
)

func newSteveCommand() *cobra.Command {
	var opts steve.Options
	cmd := &cobra.Command{
		Use:   "steve",
		Short: "Serve the steve API and the Cluster Explorer dashboard",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return steve.Run(genericapiserver.SetupSignalContext(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Path to the kubeconfig file")
	cmd.Flags().StringVar(&opts.Context, "context", "", "Kubeconfig context to use")
	return cmd
}
