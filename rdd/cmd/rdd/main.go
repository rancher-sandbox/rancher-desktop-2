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

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/console"
	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostagent"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/version"
)

// optionalCommands holds constructors for commands that only some builds
// include. Files behind a build constraint, such as steve.go, append to it from
// init(), so main.go compiles without them.
var optionalCommands []func() *cobra.Command

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
	// Route log output through RepairingWriter so a child corrupting the shared
	// console's newline mode doesn't make our lines "staircase" (see pkg/cli/console).
	logrus.SetOutput(console.RepairingWriter(os.Stderr))
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
	args, err := multiCallArgs(os.Args)
	if err != nil {
		logrus.Fatal(err)
	}
	os.Args = args
	// An rdd-<instance> name overrides RDD_INSTANCE. ParseFlag runs after it,
	// so --instance overrides both.
	if name := instanceFromName(os.Args[0]); name != "" {
		_ = os.Setenv("RDD_INSTANCE", name)
	}
	os.Args = instance.ParseFlag(os.Args)
	// One check covers RDD_INSTANCE, --instance and the command name. It runs
	// before anything else reads the instance.
	if err := instance.ValidateSuffix(instance.Suffix()); err != nil {
		logrus.Fatal(err)
	}

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

	for _, newCommand := range optionalCommands {
		cmd.AddCommand(newCommand())
	}

	cmd.AddCommand(
		hostagent.NewCommand(),
		newKubectlCommand(),
		newNerdctlCommand(),
		newRunCommand(),
		newServiceCommand(context.Background()),
		newSetCommand(),
		newStartCommand(),
		newStopCommand(),
		newVersionCommand(),
	)
	if err := cli.RunNoErrOutput(cmd); err != nil {
		// *cliexit.Error lets a subcommand opt into a specific exit code; everything else gets exit 1.
		if exitErr, ok := errors.AsType[*cliexit.Error](err); ok {
			if _, ok := logrus.StandardLogger().Formatter.(*logrus.JSONFormatter); ok {
				// If we're logging to JSON, include the exit code.
				logrus.WithField("exit-code", exitErr.Code).Error(err)
			} else if exitErr.Err != nil {
				// A propagated child-exit code carries no message (nil Err); logging it
				// would emit a bare level=error line, so log only when there is one.
				logrus.Error(err)
			}
			os.Exit(exitErr.Code)
		}
		logrus.Fatal(err)
	}
}
