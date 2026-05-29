// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"gotest.tools/v3/assert"
)

func openSelf(t *testing.T) windows.Handle {
	t.Helper()
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(os.Getpid()))
	assert.NilError(t, err)
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	return handle
}

// TestProcessCommandLineReadsOwnCommandLine exercises the PEB walk against the
// running test process, catching struct-offset regressions in the unsafe reads.
func TestProcessCommandLineReadsOwnCommandLine(t *testing.T) {
	cmdline, err := processCommandLine(openSelf(t))
	assert.NilError(t, err)
	assert.Assert(t, cmdline != "", "command line should not be empty")

	base := filepath.Base(os.Args[0])
	assert.Assert(t, strings.Contains(cmdline, base),
		"command line %q should contain argv0 base %q", cmdline, base)
}

// TestIsOurProcess confirms identity matching: the running process matches its
// own executable and command line, and rejects absent substrings and dead PIDs.
func TestIsOurProcess(t *testing.T) {
	pid := os.Getpid()
	base := filepath.Base(os.Args[0])

	assert.Assert(t, IsOurProcess(pid), "current process should match its own executable")
	assert.Assert(t, IsOurProcess(pid, base),
		"current process should match its own argv0 base %q", base)
	assert.Assert(t, !IsOurProcess(pid, "substring-that-cannot-appear-9c1f"),
		"a substring absent from the command line should not match")
	// A PID this high is never assigned, so OpenProcess fails and the result
	// must be false rather than an accidental match.
	assert.Assert(t, !IsOurProcess(0xFFFFFFF0, "hostagent"),
		"a nonexistent PID should not match")
}

// TestIsOurProcessRejectsForeignExecutable confirms the image-path check: a live
// process running a different executable is not ours, even when a requested
// substring appears in its command line. This is the branch that defends against
// PID reuse.
func TestIsOurProcessRejectsForeignExecutable(t *testing.T) {
	// ping runs for several seconds without console input, giving a stable live
	// PID backed by an executable other than the test binary.
	cmd := exec.CommandContext(t.Context(), "ping.exe", "-n", "10", "127.0.0.1")
	assert.NilError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	pid := cmd.Process.Pid

	// Confirm the rejection comes from the image-path mismatch rather than an
	// OpenProcess failure or an already-exited process: open the child the way
	// IsOurProcess does and verify its image is readable and differs from the
	// test binary. Without this, a denied OpenProcess would let the assertions
	// below pass without exercising the defense.
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	assert.NilError(t, err)
	defer func() { _ = windows.CloseHandle(handle) }()
	image, err := processImagePath(handle)
	assert.NilError(t, err)
	self, err := os.Executable()
	assert.NilError(t, err)
	assert.Assert(t, !strings.EqualFold(filepath.Clean(image), filepath.Clean(self)),
		"ping image %q must differ from the test binary %q", image, self)

	assert.Assert(t, !IsOurProcess(pid),
		"a live process running a different executable should not match")
	assert.Assert(t, !IsOurProcess(pid, "ping"),
		"a command-line substring match must not override an executable mismatch")
}
