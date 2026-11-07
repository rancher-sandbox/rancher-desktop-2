// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// ParseFlag handles the --instance flag before Cobra processes any flags. It
// sets RDD_INSTANCE from the flag and returns args with the flag removed. Only
// args[1] is examined, so --instance must come directly after the command name.
//
// Cobra's own flag parsing would come too late. This package caches instance
// names, suffixes and directories with sync.OnceValue on first access, and it
// may already be initialized by the time PersistentPreRun runs. After that, a
// change to RDD_INSTANCE does not take effect. Commands that set
// DisableFlagParsing:true, such as kubectl and ctl, also read os.Args
// directly, so --instance must be gone before they run. kubectl passes
// os.Args to kubectlcmd.NewDefaultKubectlCommand(), which fails on an unknown
// --instance flag.
func ParseFlag(args []string) []string {
	if len(args) < 2 {
		return args
	}
	if after, ok := strings.CutPrefix(args[1], "--instance="); ok {
		if after != "" {
			_ = os.Setenv("RDD_INSTANCE", after)
		}
		return append(args[:1], args[2:]...)
	}
	if args[1] == "--instance" {
		if len(args) > 2 {
			value := args[2]
			if value != "" && !strings.HasPrefix(value, "-") {
				_ = os.Setenv("RDD_INSTANCE", value)
				return append(args[:1], args[3:]...)
			}
		}
		return append(args[:1], args[2:]...)
	}
	return args
}

// Command returns a command that runs the current executable with
// --instance=<this instance> before args, so the child serves the instance
// that this process resolved.
func Command(ctx context.Context, args ...string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// os.Executable() returns the invoked path on macOS (symlinks included)
	// and for a copy anywhere. A child started from rdd-<instance> would then
	// take its instance from that name over the RDD_INSTANCE that it inherits,
	// but --instance outranks the name. The --instance=<suffix> form passes a
	// suffix that starts with "-", which the two-argument form rejects.
	return exec.CommandContext(ctx, exe, append([]string{"--instance=" + Suffix()}, args...)...), nil
}
