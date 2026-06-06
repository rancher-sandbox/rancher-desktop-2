//go:build unix

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"context"
	"errors"
	"os/exec"

	"golang.org/x/sys/unix"
)

// SetGroup configures the command to run in its own process group.
func SetGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &unix.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Interrupt sends SIGINT to the process with the given PID.
func Interrupt(pid int) error {
	return unix.Kill(pid, unix.SIGINT)
}

// IsOurProcess reports whether pid is a live process running this program's own
// executable. On Unix it is always true: a deliberate no-op, not a guard, so
// the parameters go unused (they exist for signature parity with the Windows
// build, where the check defends GenerateConsoleCtrlEvent against PID reuse — a
// console-signal hazard that does not exist on Unix).
//
// Residual risk: callers that read an on-disk PID (ha.pid from a previous
// service) can, after a crash or reboot leaves the file behind, act on a PID
// the OS recycled to an unrelated live process — SIGINT on the signal path,
// SIGKILL of its process group on the forced path. We accept it because Unix
// recycles PIDs only after the counter wraps, far less aggressively than
// Windows.
func IsOurProcess(_ int, _ ...string) bool {
	return true
}

// IsOurProcessWithArg is the whole-argument counterpart of IsOurProcess. On
// Unix it is the same deliberate no-op, returning true for signature parity
// with the Windows build.
func IsOurProcessWithArg(_ int, _ string) bool {
	return true
}

// Kill sends SIGTERM to the process with the given PID.
func Kill(pid int) error {
	return unix.Kill(pid, unix.SIGTERM)
}

// KillTree terminates the process and all its descendants.
// The target must have been started with SetGroup so it leads its own group.
// On Unix, this sends SIGKILL to the process group. On Windows, this uses
// taskkill /F /T to walk the parent-child tree. When the target is a group
// leader whose children remain in the same group (the expected usage), both
// platforms produce the same result.
//
// If the process group does not exist (the target is not a group leader),
// falls back to killing the individual process. This handles cases like
// a VM driver (e.g., QEMU) that inherited its parent's process group.
//
// Returns nil if the process (group) no longer exists.
func KillTree(_ context.Context, pid int) error {
	err := unix.Kill(-pid, unix.SIGKILL)
	if errors.Is(err, unix.ESRCH) {
		// Process group does not exist — the target may not be a group
		// leader. Fall back to killing the individual process.
		err = unix.Kill(pid, unix.SIGKILL)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
	}
	return err
}
