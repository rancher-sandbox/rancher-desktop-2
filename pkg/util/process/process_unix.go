//go:build unix

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package process provides cross-platform process utilities.
package process

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

// SetGroup configures the command to run in its own process group.
func SetGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &unix.SysProcAttr{Setpgid: true}
}

// Kill sends SIGTERM to the process with the given PID.
func Kill(pid int) error {
	return unix.Kill(pid, unix.SIGTERM)
}
