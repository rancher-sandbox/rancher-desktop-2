// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The containerd Authors

//go:build linux

package main

import (
	"github.com/containerd/nerdctl/v2/cmd/nerdctl/helpers"
	"github.com/containerd/nerdctl/v2/pkg/config"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// replicaRootCmd rebuilds nerdctl's root command. newApp and
// initRootCmdFlags live in nerdctl's package main and cannot be imported, so
// the flag registrations below are copied from initRootCmdFlags of the
// pinned nerdctl version. Stage 2 verifies the registered names against
// zz_generated_rootflags.go, so upstream drift fails generation instead of
// producing a stale table.
func replicaRootCmd() *cobra.Command {
	cfg := config.New()
	rootCmd := &cobra.Command{
		Use:              "nerdctl",
		TraverseChildren: true, // required for global short hands like -a, -H, -n
	}
	aliasToBeInherited := pflag.NewFlagSet(rootCmd.Name(), pflag.ExitOnError)

	rootCmd.PersistentFlags().Bool("debug", cfg.Debug, "debug mode")
	rootCmd.PersistentFlags().Bool("debug-full", cfg.DebugFull, "debug mode (with full output)")
	helpers.AddPersistentStringFlag(rootCmd, "address", []string{"a", "H"}, nil, []string{"host"}, aliasToBeInherited, cfg.Address, "CONTAINERD_ADDRESS", `containerd address, optionally with "unix://" prefix`)
	helpers.AddPersistentStringFlag(rootCmd, "namespace", []string{"n"}, nil, nil, aliasToBeInherited, cfg.Namespace, "CONTAINERD_NAMESPACE", `containerd namespace, such as "moby" for Docker, "k8s.io" for Kubernetes`)
	helpers.AddPersistentStringFlag(rootCmd, "snapshotter", nil, nil, []string{"storage-driver"}, aliasToBeInherited, cfg.Snapshotter, "CONTAINERD_SNAPSHOTTER", "containerd snapshotter")
	helpers.AddPersistentStringFlag(rootCmd, "cni-path", nil, nil, nil, aliasToBeInherited, cfg.CNIPath, "CNI_PATH", "cni plugins binary directory")
	helpers.AddPersistentStringFlag(rootCmd, "cni-netconfpath", nil, nil, nil, aliasToBeInherited, cfg.CNINetConfPath, "NETCONFPATH", "cni config directory")
	rootCmd.PersistentFlags().String("data-root", cfg.DataRoot, "Root directory of persistent nerdctl state (managed by nerdctl, not by containerd)")
	rootCmd.PersistentFlags().String("cgroup-manager", cfg.CgroupManager, `Cgroup manager to use ("cgroupfs"|"systemd")`)
	rootCmd.PersistentFlags().Bool("insecure-registry", cfg.InsecureRegistry, "skips verifying HTTPS certs, and allows falling back to plain HTTP")
	rootCmd.PersistentFlags().StringSlice("hosts-dir", cfg.HostsDir, "A directory that contains <HOST:PORT>/hosts.toml (containerd style) or <HOST:PORT>/{ca.cert, cert.pem, key.pem} (docker style)")
	helpers.AddPersistentBoolFlag(rootCmd, "experimental", nil, nil, cfg.Experimental, "NERDCTL_EXPERIMENTAL", "Control experimental: https://github.com/containerd/nerdctl/blob/main/docs/experimental.md")
	helpers.AddPersistentStringFlag(rootCmd, "host-gateway-ip", nil, nil, nil, aliasToBeInherited, cfg.HostGatewayIP, "NERDCTL_HOST_GATEWAY_IP", "IP address that the special 'host-gateway' string in --add-host resolves to. Defaults to the IP address of the host. It has no effect without setting --add-host")
	helpers.AddPersistentStringFlag(rootCmd, "bridge-ip", nil, nil, nil, aliasToBeInherited, cfg.BridgeIP, "NERDCTL_BRIDGE_IP", "IP address for the default nerdctl bridge network")
	rootCmd.PersistentFlags().Bool("kube-hide-dupe", cfg.KubeHideDupe, "Deduplicate images for Kubernetes with namespace k8s.io")

	addExtractedCommands(rootCmd)
	addReplicaCommands(rootCmd)

	// Copied from newApp: subcommands accept the root alias flags.
	for _, subCmd := range rootCmd.Commands() {
		subCmd.InheritedFlags().AddFlagSet(aliasToBeInherited)
	}
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()
	return rootCmd
}

// replicaCommands names the package-main constructors this file provides;
// stage 2 fails when it differs from unresolvedCommands.
var replicaCommands = []string{"newVersionCommand"}

// addReplicaCommands mirrors the commands behind unresolvedCommands.
func addReplicaCommands(rootCmd *cobra.Command) {
	versionCmd := &cobra.Command{Use: "version"}
	versionCmd.Flags().StringP("format", "f", "", "Format the output using the given Go template, e.g, '{{json .}}'")
	rootCmd.AddCommand(versionCmd)
}
