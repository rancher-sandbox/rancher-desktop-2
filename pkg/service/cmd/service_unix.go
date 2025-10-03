//go:build unix

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package service

import (
	"os/exec"

	"golang.org/x/sys/unix"
)

func killProcess(pid int) error {
	return unix.Kill(pid, unix.SIGTERM)
}

func setCommandGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &unix.SysProcAttr{Setpgid: true}
}
