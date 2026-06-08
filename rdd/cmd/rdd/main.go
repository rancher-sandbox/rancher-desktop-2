// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

// Command rdd is the Rancher Desktop Daemon CLI. It manages the control plane
// lifecycle, provides kubectl access to the embedded API server, and controls
// Lima VMs.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"k8s.io/component-base/cli"
	_ "k8s.io/component-base/logs/json/register"
	_ "k8s.io/component-base/metrics/prometheus/clientgo"
	_ "k8s.io/component-base/metrics/prometheus/version"

	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostagent"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/version"
)

// parseInstanceFlag handles --instance flag parsing before Cobra processes any flags.
//
// This special handling is required because:
//  1. The instance package (pkg/instance/instance.go) uses sync.OnceValue to cache instance
//     names, suffixes, and directories on first access
//  2. Once cached, these values never change, even if RDD_INSTANCE is modified later
//  3. Many commands (especially kubectl/ctl) use DisableFlagParsing:true and manipulate
//     os.Args directly, requiring --instance to be filtered out before they run
//  4. The kubectl command passes os.Args to kubectlcmd.NewDefaultKubectlCommand() which
//     would fail if it encountered unknown --instance flags
//
// Regular Cobra flag parsing happens too late - the instance package may already be
// initialized by the time PersistentPreRun executes, making the environment variable
// change ineffective.
//
// This function removes --instance from os.Args before Cobra processes it, ensuring
// the RDD_INSTANCE environment variable is set early enough to affect instance package
// initialization.
func parseInstanceFlag() {
	if len(os.Args) < 2 {
		return
	}
	if strings.HasPrefix(os.Args[1], "--instance=") {
		instance := strings.TrimPrefix(os.Args[1], "--instance=")
		if instance != "" {
			_ = os.Setenv("RDD_INSTANCE", instance)
		}
		os.Args = append(os.Args[:1], os.Args[2:]...)
		return
	}
	if os.Args[1] == "--instance" {
		if len(os.Args) > 2 {
			instance := os.Args[2]
			if instance != "" && !strings.HasPrefix(instance, "-") {
				_ = os.Setenv("RDD_INSTANCE", instance)
				os.Args = append(os.Args[:1], os.Args[3:]...)
				return
			}
		}
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
}

// setLogOptions sets the log level from command flags or RDD_LOG_LEVEL.
func setLogOptions(cmd *cobra.Command, _ []string) error {
	if logFormat, err := cmd.Root().Flags().GetString("log-format"); err == nil {
		switch logFormat {
		case "text", "":
			// The default log format is already text; nothing to do here.
		case "json":
			logrus.SetFormatter(&logrus.JSONFormatter{})
		default:
			return fmt.Errorf("invalid log format: %q", logFormat)
		}
	}
	logLevel, err := cmd.Root().Flags().GetString("log-level")
	if err != nil {
		return err
	}
	if logLevel == "" {
		logLevel = strings.TrimSpace(os.Getenv("RDD_LOG_LEVEL"))
	}
	if logLevel == "" {
		// Default log level: warn for regular mode, debug for developer mode
		if developer.Mode() {
			logLevel = "debug"
		} else {
			logLevel = "warn"
		}
	}
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		return err
	}
	logrus.SetLevel(level)
	return nil
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print RDD version information",
		Long:  help.Doc(`Print RDD version information and exit.`),
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.Get().String())
		},
	}
}

func main() {
	parseInstanceFlag()

	cmd := &cobra.Command{
		Use:   "rdd",
		Short: "Rancher Desktop Daemon",
		Long: help.Doc(`
			RDD manages the Rancher Desktop 2 background services.
		`),
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: setLogOptions,
	}

	// Add version flag with verflag-compatible behavior
	version.AddVersionFlag(cmd.Flags())

	// Add global log-level flag
	var levels []string
	for _, level := range logrus.AllLevels {
		if level != logrus.PanicLevel {
			levels = append(levels, level.String())
		}
	}
	cmd.PersistentFlags().String("log-level", "", "Log level: "+strings.Join(levels, ", "))
	cmd.PersistentFlags().String("log-format", "text", "Log format: text or json")

	ctlCmd := newCtlCommand()
	ctlCmd.Hidden = !developer.Mode()
	cmd.AddCommand(ctlCmd)

	limaCmd := newLimaVMCommand()
	limaCmd.Hidden = !developer.Mode()
	cmd.AddCommand(limaCmd)

	yqCmd := newYQCommand()
	yqCmd.Hidden = !developer.Mode()
	cmd.AddCommand(yqCmd)

	cmd.AddCommand(
		hostagent.NewCommand(),
		newKubectlCommand(),
		newRunCommand(),
		newServiceCommand(context.Background()),
		newSetCommand(),
		newStartCommand(),
		newStopCommand(),
		newVersionCommand(),
	)
	if err := cli.RunNoErrOutput(cmd); err != nil {
		// *cliexit.Error lets a subcommand opt into a specific exit code; everything else gets exit 1.
		var exitErr *cliexit.Error
		if errors.As(err, &exitErr) {
			// A propagated child-exit code carries no message (nil Err); logging it
			// would emit a bare level=error line, so log only when there is one.
			if exitErr.Err != nil {
				logrus.Error(err)
			}
			os.Exit(exitErr.Code)
		}
		logrus.Fatal(err)
	}
}
