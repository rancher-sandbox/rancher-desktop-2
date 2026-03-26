// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package process provides cross-platform process utilities.
package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

// SetGroup configures the command to run in its own process group.
// On Windows, CREATE_NEW_PROCESS_GROUP allows GenerateConsoleCtrlEvent to
// target only the child process (using its PID as the group ID) without
// affecting the parent process.
func SetGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// Interrupt sends a graceful shutdown signal to the process with the given PID.
// On Windows, this uses GenerateConsoleCtrlEvent with CTRL_BREAK_EVENT targeted
// at the process group (requires CREATE_NEW_PROCESS_GROUP via SetGroup).
func Interrupt(pid int) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
}

// Kill terminates the process with the given PID.
func Kill(pid int) error {
	hProcess, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.SYNCHRONIZE,
		false,
		uint32(pid))
	if err != nil {
		return fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer func() {
		_ = windows.CloseHandle(hProcess)
	}()
	if err := windows.TerminateProcess(hProcess, 1); err != nil {
		return fmt.Errorf("failed to terminate process %d: %w", pid, err)
	}
	result, err := windows.WaitForSingleObject(hProcess, uint32(10*time.Second/time.Millisecond))
	if err != nil {
		return fmt.Errorf("failed waiting for process %d to terminate: %w", pid, err)
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("timed out waiting for process %d to terminate", pid)
	}

	return nil
}

// taskkillExitNotFound is the exit code taskkill returns when the target
// process does not exist. Not officially documented by Microsoft.
const taskkillExitNotFound = 128

// KillTree terminates the process and all its descendants.
// The target must have been started with SetGroup so it leads its own group.
// On Windows, this uses taskkill /F /T to walk the parent-child tree. On
// Unix, this sends SIGKILL to the process group. When the target is a group
// leader whose children remain in the same group (the expected usage), both
// platforms produce the same result.
//
// Platform asymmetry: if the target process is already dead, taskkill /T
// returns exit code 128 (treated as success), but surviving children (e.g.,
// SSH port forwarders) are not killed because taskkill cannot traverse the
// tree from a dead parent. On Unix, kill(-pgid) still reaches all group
// members. This is acceptable: orphaned port forwarders cannot rebind their
// ports and are harmless. Windows Job Objects would fix this if needed.
//
// Returns nil if the process no longer exists (taskkill exit code 128).
func KillTree(ctx context.Context, pid int) error {
	err := exec.CommandContext(ctx, "taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == taskkillExitNotFound {
		return nil
	}
	return err
}
