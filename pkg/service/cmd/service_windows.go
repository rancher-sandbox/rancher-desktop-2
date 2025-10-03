// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package service

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

func killProcess(pid int) error {
	hProcess, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer func() {
		_ = windows.CloseHandle(hProcess)
	}()
	return windows.TerminateProcess(hProcess, 1)
}

func setCommandGroup(*exec.Cmd) {
	// TODO: implement
}
