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

// TestCommandLineHasArg confirms whole-argument matching: a token is matched
// only as a complete argument in argv[1:], never as a substring of argv[0] or
// of another argument. This is the property that keeps the service-PID guard
// from accepting an unrelated rdd process whose image path contains "serve".
func TestCommandLineHasArg(t *testing.T) {
	assert.Assert(t, commandLineHasArg(`C:\rdd.exe service serve --secure-port 6443`, "serve"),
		"the canonical self-spawn carries serve as an argument")
	assert.Assert(t, commandLineHasArg(`C:\rdd.exe svc serve`, "serve"),
		"the svc alias also carries serve as an argument")
	assert.Assert(t, commandLineHasArg(`"C:\Program Files\rdd\rdd.exe" service serve`, "serve"),
		"a quoted image path with spaces stays a single argv[0]")
	// argv[0] containing the token as a substring must not match.
	assert.Assert(t, !commandLineHasArg(`C:\observer\rdd.exe ctl get`, "serve"),
		"serve inside the image path is not an argument")
	assert.Assert(t, !commandLineHasArg(`C:\webserver\rdd.exe ctl`, "serve"),
		"serve inside the image path is not an argument")
	// Another rdd subcommand is not the control plane.
	assert.Assert(t, !commandLineHasArg(`C:\rdd.exe hostagent --pidfile C:\x\ha.pid vm`, "serve"),
		"the hostagent subcommand is not serve")
	// argv[0] alone, with no arguments, never matches.
	assert.Assert(t, !commandLineHasArg(`C:\rdd.exe`, "serve"),
		"a bare image path has no arguments to match")
}

// TestIsOurProcessWithArg confirms the whole-argument identity check: the argv0
// base name is a substring of the command line but not an argument, so it must
// not match — the behaviour that distinguishes IsOurProcessWithArg from
// IsOurProcess and closes the image-path over-match.
func TestIsOurProcessWithArg(t *testing.T) {
	pid := os.Getpid()
	base := filepath.Base(os.Args[0])

	assert.Assert(t, !IsOurProcessWithArg(pid, base),
		"the argv0 base %q is part of argv0, not an argument", base)
	assert.Assert(t, !IsOurProcessWithArg(pid, "arg-that-cannot-appear-9c1f"),
		"an argument absent from the command line should not match")
	assert.Assert(t, !IsOurProcessWithArg(0xFFFFFFF0, "serve"),
		"a nonexistent PID should not match")
	if len(os.Args) > 1 {
		assert.Assert(t, IsOurProcessWithArg(pid, os.Args[1]),
			"the running process should match its own argument %q", os.Args[1])
	}
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

// TestSamePathMatchesShortAndLongForms confirms samePath treats an 8.3 short
// path and its long form as the same executable. A user can launch rdd through
// either form, so the identity check must recognize a live control plane no
// matter which form named the process being checked.
func TestSamePathMatchesShortAndLongForms(t *testing.T) {
	// A directory name with no natural 8.3 form makes Windows synthesize a
	// distinct short name where 8.3 generation is enabled on the volume.
	longDir := filepath.Join(t.TempDir(), "LongDirectoryNameBeyond8dot3")
	assert.NilError(t, os.Mkdir(longDir, 0o755))
	longFile := filepath.Join(longDir, "control-plane-binary.exe")
	assert.NilError(t, os.WriteFile(longFile, []byte("stub"), 0o644))

	short := shortPathName(t, longFile)
	if strings.EqualFold(filepath.Clean(short), filepath.Clean(longFile)) {
		t.Skip("8.3 short-name generation is disabled on this volume")
	}
	assert.Assert(t, samePath(short, longFile),
		"short form %q and long form %q must resolve to the same executable", short, longFile)
}

// shortPathName returns the 8.3 short form of p via GetShortPathName.
func shortPathName(t *testing.T, p string) string {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(p)
	assert.NilError(t, err)
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(p16, &buf[0], uint32(len(buf)))
	assert.NilError(t, err)
	assert.Assert(t, n < uint32(len(buf)), "short-path buffer too small")
	return windows.UTF16ToString(buf[:n])
}
