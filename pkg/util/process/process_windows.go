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
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

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

// IsOurProcess reports whether pid is a live process running this program's own
// executable with a command line that contains every string in cmdlineSubstrings.
//
// It guards graceful shutdown via Interrupt against PID reuse. Windows recycles
// PIDs aggressively, and GenerateConsoleCtrlEvent(CTRL_BREAK) delivered to a
// recycled PID can reach unrelated processes that share the console — including
// the rdd service and the controlling terminal — rather than the intended
// target. Callers confirm identity here before signalling; on a mismatch they
// fall back to KillTree, which terminates a single PID and cannot escape to the
// console.
//
// It returns false (never an error) whenever identity cannot be positively
// confirmed — the process is gone, inaccessible, or no longer ours — so callers
// treat "unknown" as "not ours" and never signal it.
//
// Each substring is matched with strings.Contains, not as a whole argument, so a
// discriminator must be specific enough that it cannot appear in an unrelated
// process's command line. An instance name that is a prefix of another (e.g.
// "vm" and "vm2") matches both; pass a name distinct enough to avoid that.
//
// With no substrings the image-path match alone decides the result, which a
// recycled PID running any other rdd process would satisfy; production callers
// should always pass a discriminator.
func IsOurProcess(pid int, cmdlineSubstrings ...string) bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	image, err := processImagePath(handle)
	if err != nil || !strings.EqualFold(filepath.Clean(image), filepath.Clean(self)) {
		return false
	}
	cmdline, err := processCommandLine(handle)
	if err != nil {
		return false
	}
	for _, s := range cmdlineSubstrings {
		if !strings.Contains(cmdline, s) {
			return false
		}
	}
	return true
}

// processImagePath returns the full path to the executable backing the process.
// QueryFullProcessImageName does not report the size it needs on failure, so on
// ERROR_INSUFFICIENT_BUFFER this doubles the buffer and retries, up to the
// Windows extended-path ceiling of 32767 characters.
func processImagePath(handle windows.Handle) (string, error) {
	for bufSize := 1024; bufSize <= 32768; bufSize *= 2 {
		buf := make([]uint16, bufSize)
		size := uint32(len(buf))
		err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size)
		if err == nil {
			return windows.UTF16ToString(buf[:size]), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return "", err
		}
	}
	return "", windows.ERROR_INSUFFICIENT_BUFFER
}

// processCommandLine reads the target process's command line out of its PEB.
// Windows exposes no API for another process's command line, so we follow the
// documented PEB -> RTL_USER_PROCESS_PARAMETERS -> CommandLine chain via
// ReadProcessMemory. The handle must carry PROCESS_VM_READ.
func processCommandLine(handle windows.Handle) (string, error) {
	var pbi windows.PROCESS_BASIC_INFORMATION
	var retLen uint32
	if err := windows.NtQueryInformationProcess(handle, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &retLen); err != nil {
		return "", err
	}

	var peb windows.PEB
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(pbi.PebBaseAddress)),
		unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return "", err
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(peb.ProcessParameters)),
		unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return "", err
	}

	length := params.CommandLine.Length
	if length == 0 || params.CommandLine.Buffer == nil {
		return "", nil
	}
	// NTUnicodeString.Length counts bytes; the buffer holds UTF-16 code units.
	buf := make([]uint16, length/2)
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(params.CommandLine.Buffer)),
		unsafe.Pointer(&buf[0]), uintptr(length)); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

// readProcessMemory copies size bytes at addr in the target process into dest,
// erroring unless the full range was read.
func readProcessMemory(handle windows.Handle, addr uintptr, dest unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(handle, addr, (*byte)(dest), size, &read); err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("short read at %#x: got %d of %d bytes", addr, read, size)
	}
	return nil
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
