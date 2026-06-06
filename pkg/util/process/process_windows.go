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
	"slices"
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
// should always pass a discriminator. To match a short token that could also
// appear inside the image path, use IsOurProcessWithArg, which compares whole
// arguments instead of substrings.
func IsOurProcess(pid int, cmdlineSubstrings ...string) bool {
	cmdline, ok := ourProcessCommandLine(pid)
	if !ok {
		return false
	}
	for _, s := range cmdlineSubstrings {
		if !strings.Contains(cmdline, s) {
			return false
		}
	}
	return true
}

// IsOurProcessWithArg reports whether pid is a live process running this
// program's own executable with arg as one of its command-line arguments
// (argv[1:], excluding the executable path). Unlike IsOurProcess, which matches
// a raw substring of the whole command line, this matches a complete argument,
// so a short token such as a subcommand name cannot be satisfied by an
// unrelated process whose image path merely contains that text.
//
// It shares IsOurProcess's identity guarantees and failure semantics: it
// returns false (never an error) whenever identity cannot be positively
// confirmed, so callers treat "unknown" as "not ours". On non-Windows it is a
// no-op that returns true, like IsOurProcess.
func IsOurProcessWithArg(pid int, arg string) bool {
	cmdline, ok := ourProcessCommandLine(pid)
	if !ok {
		return false
	}
	return commandLineHasArg(cmdline, arg)
}

// commandLineHasArg reports whether arg appears as a complete argument in
// commandLine, ignoring argv[0] (the executable path). It parses the command
// line with the same rules Windows uses (CommandLineToArgvW, via
// DecomposeCommandLine), so a quoted path containing spaces stays a single
// argument and a token that is only a substring of argv[0] is not matched.
func commandLineHasArg(commandLine, arg string) bool {
	args, err := windows.DecomposeCommandLine(commandLine)
	if err != nil || len(args) < 2 {
		return false
	}
	return slices.Contains(args[1:], arg)
}

// ourProcessCommandLine returns pid's command line when pid is a live process
// running this program's own executable, and false otherwise. It centralises
// the OpenProcess and image-path comparison that the identity checks share.
func ourProcessCommandLine(pid int) (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	image, err := processImagePath(handle)
	if err != nil || !samePath(image, self) {
		return "", false
	}
	cmdline, err := processCommandLine(handle)
	if err != nil {
		return "", false
	}
	return cmdline, true
}

// samePath reports whether image and self name the same on-disk executable.
// It resolves each to its long (non-8.3) form first, so a process launched
// through a short path such as C:\PROGRA~1\... still matches the same binary
// named by its long path. The two rdd processes compared need not have been
// launched the same way.
func samePath(image, self string) bool {
	return strings.EqualFold(longPath(image), longPath(self))
}

// longPath returns p in its long, non-8.3 form. If the long form cannot be
// resolved (for example the file no longer exists), it falls back to
// filepath.Clean(p) so a transient resolution failure degrades to a lexical
// comparison rather than a guaranteed mismatch — for the service PID a false
// "not ours" would orphan a live control plane.
func longPath(p string) string {
	if resolved, err := resolveLongPath(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// resolveLongPath expands any 8.3 short components of p via GetLongPathName,
// growing the buffer up to the Windows extended-path ceiling the way
// processImagePath does.
func resolveLongPath(p string) (string, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	for bufSize := uint32(1024); bufSize <= 32768; bufSize *= 2 {
		buf := make([]uint16, bufSize)
		n, err := windows.GetLongPathName(p16, &buf[0], bufSize)
		if err != nil {
			return "", err
		}
		if n < bufSize {
			return windows.UTF16ToString(buf[:n]), nil
		}
		// n >= bufSize: the buffer was too small and n is the required size; grow.
	}
	return "", windows.ERROR_INSUFFICIENT_BUFFER
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
	// Under one UTF-16 unit — including an impossible odd byte count — there is
	// no command line to read, and length/2 would size buf to zero and panic the
	// &buf[0] deref below. Treat it as empty.
	if length < 2 || params.CommandLine.Buffer == nil {
		return "", nil
	}
	// NTUnicodeString.Length counts bytes; the buffer holds UTF-16 code units.
	// Read len(buf)*2 bytes rather than Length itself, so the read can never
	// exceed the buffer if Length is ever odd — it is not on supported Windows,
	// but pairing the read size to the buffer makes the invariant explicit.
	buf := make([]uint16, length/2)
	if err := readProcessMemory(handle, uintptr(unsafe.Pointer(params.CommandLine.Buffer)),
		unsafe.Pointer(&buf[0]), uintptr(len(buf)*2)); err != nil {
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
