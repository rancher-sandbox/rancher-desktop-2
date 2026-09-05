// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The Lima Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"al.essio.dev/pkg/shellescape"
	"github.com/coreos/go-semver/semver"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/sshutil"
	"github.com/lima-vm/lima/v2/pkg/store"
	"github.com/mattn/go-isatty"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/nerdctlstub"
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

// limaVMGuestExec runs a command (or an interactive shell when command is
// empty) in a Lima VM over ssh, wiring up the caller's stdio and
// propagating the remote exit code.
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
	inst, err := store.Inspect(ctx, instanceName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("instance %q does not exist on disk", instanceName)
		}
		return err
	}
	if len(inst.Errors) > 0 {
		return fmt.Errorf("instance %q has configuration errors: %w", instanceName, errors.Join(inst.Errors...))
	}
	if inst.Config == nil {
		return fmt.Errorf("instance %q has no configuration", instanceName)
	}
	if inst.Status != limatype.StatusRunning {
		return fmt.Errorf("instance %q is not running (status: %s), use 'rdd lima start %s' first", instanceName, inst.Status, instanceName)
	}

	// Build working directory change command
	var changeDirCmd string
	if workDir != "" {
		changeDirCmd = fmt.Sprintf("cd %s || exit 1", shellescape.Quote(workDir))
	} else if len(inst.Config.Mounts) > 0 || runtime.GOOS == "windows" {
		hostCurrentDir, err := os.Getwd()
		if err == nil {
			// On Windows the guest mounts drives under /mnt, so a host
			// path like C:\work maps to /mnt/c/work; elsewhere host
			// mounts keep their own path and translation passes them
			// through unchanged.
			hostCurrentDir, err = nerdctlstub.TranslateHostPath(hostCurrentDir)
		}
		if err == nil {
			changeDirCmd = fmt.Sprintf("cd %s", shellescape.Quote(hostCurrentDir))
		} else {
			changeDirCmd = "false"
			logrus.WithError(err).Warn("failed to get the current directory")
		}
		hostHomeDir, err := os.UserHomeDir()
		if err == nil {
			hostHomeDir, err = nerdctlstub.TranslateHostPath(hostHomeDir)
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
	sshExe, err := sshutil.NewSSHExe()
	if err != nil {
		return err
	}

	sshOpts, err := sshutil.SSHOpts(
		ctx,
		sshExe,
		inst.Dir,
		*inst.Config.User.Name,
		*inst.Config.SSH.LoadDotSSHPubKeys,
		*inst.Config.SSH.ForwardAgent,
		*inst.Config.SSH.ForwardX11,
		*inst.Config.SSH.ForwardX11Trusted)
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		sshOpts = sshutil.SSHOptsRemovingControlPath(sshOpts)
	}

	sshArgs := append([]string{}, sshExe.Args...)
	sshArgs = append(sshArgs, sshutil.SSHArgsFromOpts(sshOpts)...)

	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		sshArgs = append(sshArgs, "-t")
	}

	if _, present := os.LookupEnv("COLORTERM"); present {
		sshArgs = append(sshArgs, "-o", "SendEnv=COLORTERM")
	}

	logLevel := "ERROR"
	olderSSH := sshutil.DetectOpenSSHVersion(ctx, sshExe).LessThan(*semver.New("8.9.0"))
	if olderSSH {
		logLevel = "QUIET"
	}

	// ConnectTimeout caps the TCP handshake at 30s. ServerAliveInterval=30
	// with ServerAliveCountMax=3 closes a wedged session after ~90s of
	// unanswered keep-alives. Interactive shells and long-running commands
	// ack the keep-alives and stay connected.
	sshArgs = append(sshArgs, []string{
		"-o", fmt.Sprintf("LogLevel=%s", logLevel),
		"-o", "ConnectTimeout=30",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-p", strconv.Itoa(inst.SSHLocalPort),
		inst.SSHAddress,
		"--",
		script,
	}...)

	sshCmd := exec.CommandContext(ctx, sshExe.Exe, sshArgs...)
	sshCmd.Stdin = os.Stdin
	sshCmd.Stdout = os.Stdout
	sshCmd.Stderr = os.Stderr

	logrus.Debugf("executing ssh: %+v", sshCmd.Args)

	err = sshCmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// ssh exits with the remote command's exit code (or 255 for
		// connection errors); propagate it.
		return &cliexit.Error{Code: exitErr.ExitCode()}
	}
	return err
}
