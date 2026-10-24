// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	cliflag "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/flag"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// instancePaths maps each `rdd svc paths` key to an instance path, so bats
// tests and the app read the paths rdd uses instead of building their own.
func instancePaths() map[string]string {
	return map[string]string{
		"dir":                instance.Dir(),
		"log_dir":            instance.LogDir(),
		"stdout_log":         instance.StdoutLog(),
		"stderr_log":         instance.StderrLog(),
		"short_dir":          instance.ShortDir(),
		"bin_dir":            instance.BinDir(),
		"path_marker":        instance.PathMarker(),
		"lima_home":          instance.LimaHome(),
		"tls_dir":            instance.TLSDir(),
		"config":             instance.Config(),
		"k3s_config":         instance.K3sConfig(),
		"pid_file":           instance.PIDFile(),
		"args_file":          instance.ArgsFile(),
		"docker_socket":      instance.DockerSocket(),
		"containerd_socket":  instance.ContainerdSocket(),
		"docker_cli_plugins": instance.DockerCliPluginsDir(),
	}
}

const (
	outputTable = "table"
	outputJSON  = "json"
	outputShell = "shell"
)

func newServicePathsCommand() *cobra.Command {
	output := cliflag.NewEnumValue(outputTable, outputJSON, outputShell)
	command := &cobra.Command{
		Use:   "paths [key]",
		Short: "Print instance paths",
		Args:  cobra.MaximumNArgs(1),
		RunE:  servicePathsAction,
	}
	command.Flags().VarP(output, "output", "o", "Output format: "+output.AllowedValues())
	return command
}

func servicePathsAction(cmd *cobra.Command, args []string) error {
	paths := instancePaths()
	keys := slices.Sorted(maps.Keys(paths))
	output := cmd.Flags().Lookup("output").Value.String()

	// Filter to a single key if specified.
	if len(args) == 1 {
		key := args[0]
		if _, ok := paths[key]; !ok {
			return fmt.Errorf("unknown key %q; valid keys: %s", key, strings.Join(keys, ", "))
		}
		if output == outputTable {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), paths[key])
			return err
		}
		keys = []string{key}
	}

	w := cmd.OutOrStdout()
	switch output {
	case outputJSON:
		m := make(map[string]string, len(keys))
		for _, key := range keys {
			m[key] = paths[key]
		}
		return json.NewEncoder(w).Encode(m)
	case outputShell:
		for _, key := range keys {
			if _, err := fmt.Fprintf(w, "export RDD_%s=%q\n", strings.ToUpper(key), paths[key]); err != nil {
				return err
			}
		}
		return nil
	default:
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, key := range keys {
			fmt.Fprintf(tw, "%s\t%s\n", key, paths[key])
		}
		return tw.Flush()
	}
}
