// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// SetGroup configures the command to run in its own process group.
// On Windows, CREATE_NEW_PROCESS_GROUP detaches the child from the parent's
// Ctrl-C/Ctrl-Break, so a backgrounded daemon or hostagent is not torn down
// when the launching console receives one. Detachment is its only role here:
// graceful shutdown goes through Interrupt's named event (see Interrupt and
// RegisterInterruptHandler), independent of the process group.
func SetGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &windows.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// interruptEventName returns the name of the event Interrupt signals to ask the
// process identified by (key, pid) to shut down gracefully. The Local\ namespace
// scopes the event to the current session, which is where the control plane and
// the CLI run (same user, interactive session); a daemon running as a session-0
// Windows service would need the Global\ namespace. instance.Suffix() namespaces
// the event by rdd instance and key by role (see ServeInterruptKey /
// HostagentInterruptKey), so a PID recycled to a different instance or role
// creates no matching event and IsOurProcess can confirm both.
func interruptEventName(key string, pid int) string {
	return fmt.Sprintf(`Local\rdd-%s-interrupt-%s-%d`, instance.Suffix(), key, pid)
}

// openInterruptEvent opens the interrupt event for (key, pid). It fails when no
// process registered that event — the target is gone, recycled, unrelated, or
// not playing the keyed role — which both Interrupt and IsOurProcess rely on.
func openInterruptEvent(key string, pid int) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(interruptEventName(key, pid))
	if err != nil {
		return 0, err
	}
	return windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
}

// Interrupt asks the process identified by (key, pid) to shut down gracefully by
// signalling its named interrupt event (see RegisterInterruptHandler). A named
// event reaches the target regardless of which console either party is attached
// to, so `rdd svc delete` can gracefully stop a daemon the GUI app started in
// another console.
//
// It is also inherently safe against PID reuse: only an RDD process that called
// RegisterInterruptHandler with this key creates the event, so OpenEvent fails
// for a recycled or unrelated PID and Interrupt returns an error rather than
// disturbing it. Callers fall back to Kill/KillTree on error.
func Interrupt(key string, pid int) error {
	handle, err := openInterruptEvent(key, pid)
	if err != nil {
		return fmt.Errorf("open interrupt event %q for pid %d: %w", key, pid, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	return windows.SetEvent(handle)
}

// RegisterInterruptHandler creates this process's named interrupt event for key
// and runs onInterrupt when another process signals it via Interrupt. Every RDD
// process that can be the target of Interrupt — the control plane daemon
// (ServeInterruptKey) and each hostagent (HostagentInterruptKey) — must call
// this once at startup with its own key; the returned function stops the watcher
// and releases the event. On Unix it is a no-op: Interrupt there sends SIGINT,
// which the process already handles via signal.Notify / SetupSignalContext.
func RegisterInterruptHandler(key string, onInterrupt func()) (func(), error) {
	name, err := windows.UTF16PtrFromString(interruptEventName(key, os.Getpid()))
	if err != nil {
		return nil, err
	}
	// Manual-reset so the signal latches: the watcher cannot miss a caller's
	// SetEvent by racing it, and a later look still sees the event set.
	evt, err := windows.CreateEvent(nil, 1, 0, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, fmt.Errorf("create interrupt event: %w", err)
	}
	// Unnamed companion event, used only to wake the watcher on release.
	release, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(evt)
		return nil, fmt.Errorf("create release event: %w", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// WAIT_OBJECT_0 == the interrupt event fired; WAIT_OBJECT_0+1 == release.
		ret, waitErr := windows.WaitForMultipleObjects(
			[]windows.Handle{evt, release}, false, windows.INFINITE)
		if waitErr == nil && ret == windows.WAIT_OBJECT_0 {
			onInterrupt()
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = windows.SetEvent(release)
			<-done
			_ = windows.CloseHandle(evt)
			_ = windows.CloseHandle(release)
		})
	}, nil
}

// IsOurProcess reports whether pid is a live RDD process that registered an
// interrupt handler under key — that is, whether its per-process interrupt event
// exists. Only the daemon (ServeInterruptKey) and hostagents
// (HostagentInterruptKey) register, so a match confirms both that the process is
// ours and that it plays the expected role; a recycled or unrelated PID has no
// such event. It never errors: anything it cannot positively confirm reads as
// false ("not ours"). On non-Windows it is a no-op that returns true.
func IsOurProcess(key string, pid int) bool {
	handle, err := openInterruptEvent(key, pid)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}

// IsAlive reports whether a process with the given PID currently exists. It is
// used to decide whether a recorded PID is merely stale (the process is gone, so
// its PID file can be cleaned up) or still running (leave the file alone). A
// process we may open but not synchronize on (a higher-integrity one) counts as
// alive.
func IsAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	result, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && result == uint32(windows.WAIT_TIMEOUT)
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
