// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The Lima Authors

package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestexec"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func newLimaVMShellCommand() *cobra.Command {
	shellCmd := &cobra.Command{
		Use:           "shell INSTANCE [COMMAND...]",
		Short:         "Execute shell in Lima VM",
		Long:          "Open an interactive shell or execute a command in a Lima VM instance.",
		Args:          cobra.MinimumNArgs(1),
		RunE:          limaVMShellAction,
		SilenceErrors: true,
	}

	shellCmd.Flags().SetInterspersed(false)
	shellCmd.Flags().String("shell", "", "Shell interpreter, e.g. /bin/bash")
	shellCmd.Flags().String("workdir", "", "Working directory")

	return shellCmd
}

func limaVMShellAction(cmd *cobra.Command, args []string) error {
	logrus.SetLevel(logrus.InfoLevel)
	shell, err := cmd.Flags().GetString("shell")
	if err != nil {
		return err
	}
	workDir, err := cmd.Flags().GetString("workdir")
	if err != nil {
		return err
	}
	return limaVMGuestExec(cmd.Context(), args[0], shell, workDir, args[1:])
}

// limaVMGuestExec runs command in a Lima VM over ssh, or an interactive shell
// when command is empty. It connects the caller's stdio and returns the remote
// exit code.
func limaVMGuestExec(ctx context.Context, instanceName, shell, workDir string, command []string) error {
	// Validate the VM exists in the API server
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}
	_, err = findLimaVM(ctx, c, instanceName)
	if err != nil {
		return err
	}

	// Set LIMA_HOME for the Lima library
	if err := os.Setenv("LIMA_HOME", instance.LimaHome()); err != nil {
		return fmt.Errorf("failed to set LIMA_HOME: %w", err)
	}

	// Get the Lima instance from the store
	inst, err := guestexec.Inspect(ctx, instanceName)
	if err != nil {
		return err
	}

	// Build working directory change command
	var changeDirCmd string
	if workDir != "" {
		changeDirCmd = fmt.Sprintf("cd %s || exit 1", shellescape.Quote(workDir))
	} else if len(inst.Config.Mounts) > 0 || runtime.GOOS == "windows" {
		// The WSL2 guest sees every host drive under /mnt, whatever the
		// template's mounts say.
		hostCurrentDir, err := os.Getwd()
		if err == nil {
			hostCurrentDir, err = guestDir(hostCurrentDir)
		}
		if err == nil {
			changeDirCmd = fmt.Sprintf("cd %s", shellescape.Quote(hostCurrentDir))
		} else {
			changeDirCmd = "false"
			logrus.WithError(err).Warn("failed to get the current directory")
		}
		hostHomeDir, err := os.UserHomeDir()
		if err == nil {
			hostHomeDir, err = guestDir(hostHomeDir)
		}
		if err == nil {
			changeDirCmd = fmt.Sprintf("%s || cd %s", changeDirCmd, shellescape.Quote(hostHomeDir))
		} else {
			logrus.WithError(err).Warn("failed to get the home directory")
		}
	} else {
		logrus.Debug("the host home does not seem mounted, so the guest shell will have a different cwd")
	}

	if changeDirCmd == "" {
		changeDirCmd = "false"
	}
	logrus.Debugf("changeDirCmd=%q", changeDirCmd)

	// Determine shell
	if shell == "" {
		shell = `"$SHELL"`
	} else {
		shell = shellescape.Quote(shell)
	}

	// Build script
	script := fmt.Sprintf("%s ; exec %s --login", changeDirCmd, shell)
	if len(command) > 0 {
		quotedArgs := make([]string, len(command))
		for i, arg := range command {
			quotedArgs[i] = shellescape.Quote(arg)
		}
		script += fmt.Sprintf(" -c %s", shellescape.Quote(strings.Join(quotedArgs, " ")))
	}

	// Build SSH command
	var opts guestexec.Options
	opts.TTY = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
	if _, present := os.LookupEnv("COLORTERM"); present {
		opts.SendEnv = []string{"COLORTERM"}
	}

	sshCmd, err := guestexec.Command(ctx, inst, script, opts)
	if err != nil {
		return err
	}
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	logrus.Debugf("executing ssh: %+v", sshCmd.Args)

	err = sshCmd.Run()
	// ssh exits with the remote command's exit code, or 255 when ssh itself
	// fails.
	if exitErr := cliexit.ChildExit(err); exitErr != nil {
		return exitErr
	}
	return err
}

// guestDir returns the path at which the guest sees the host directory dir.
// Only the WSL2 guest on Windows needs a translation.
func guestDir(dir string) (string, error) {
	if runtime.GOOS != "windows" {
		return dir, nil
	}
	return guestexec.TranslateHostPath(dir)
}
