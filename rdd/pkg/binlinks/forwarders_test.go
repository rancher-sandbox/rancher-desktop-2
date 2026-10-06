// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package binlinks

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

// bundleDir returns a staged bundle directory with the forwarder, the given
// bundled tools, and a subdirectory that must be ignored. It returns the path to
// the bundled rdd executable.
func bundleDir(t *testing.T, tools ...string) string {
	t.Helper()
	srcDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, forwarderName), []byte("FORWARDER"), 0o755))
	for _, name := range tools {
		assert.NilError(t, os.WriteFile(filepath.Join(srcDir, name), []byte("binary:"+name), 0o755))
	}
	assert.NilError(t, os.Mkdir(filepath.Join(srcDir, "subdir"), 0o755))
	return filepath.Join(srcDir, "rdd.exe")
}

// assertForwarder checks that tool in binDir is a hardlink to the master
// forwarder and that its shim names target.
func assertForwarder(t *testing.T, binDir, tool, target string) {
	t.Helper()
	assertHardlink(t, filepath.Join(binDir, tool), filepath.Join(binDir, forwarderName))
	data, err := os.ReadFile(shimPath(binDir, tool))
	assert.NilError(t, err, "read shim for %q", tool)
	assert.Equal(t, string(data), "path = "+target+"\r\n")
}

func TestLinkForwarders(t *testing.T) {
	execPath := bundleDir(t, "rdd.exe", "docker.exe", "helm.exe")
	srcDir := filepath.Dir(execPath)
	binDir := filepath.Join(t.TempDir(), "bin")

	assert.NilError(t, linkForwarders(execPath, binDir))

	// Each bundled tool forwards to itself; kubectl forwards to rdd.
	assertForwarder(t, binDir, "docker.exe", filepath.Join(srcDir, "docker.exe"))
	assertForwarder(t, binDir, "helm.exe", filepath.Join(srcDir, "helm.exe"))
	assertForwarder(t, binDir, "rdd.exe", filepath.Join(srcDir, "rdd.exe"))
	assertForwarder(t, binDir, "kubectl.exe", filepath.Join(srcDir, "rdd.exe"))

	// The master copy matches the bundled forwarder and the forwarder is not
	// itself forwarded.
	master, err := os.ReadFile(filepath.Join(binDir, forwarderName))
	assert.NilError(t, err)
	assert.Equal(t, string(master), "FORWARDER")
	_, err = os.Lstat(filepath.Join(binDir, "rdd-forwarder.shim"))
	assert.Assert(t, os.IsNotExist(err), "the forwarder got a shim")

	// Exactly the forwarder, four tools, and their four shims.
	assertDirEntries(t, binDir, []string{
		forwarderName,
		"docker.exe", "helm.exe", "rdd.exe", "kubectl.exe",
		"docker.shim", "helm.shim", "rdd.shim", "kubectl.shim",
	})
}

// TestLinkForwardersReconciles checks that a second run keeps existing
// forwarders in place, refreshes a changed forwarder copy, and leaves no .old
// clutter.
func TestLinkForwardersReconciles(t *testing.T) {
	execPath := bundleDir(t, "rdd.exe", "docker.exe")
	binDir := filepath.Join(t.TempDir(), "bin")

	assert.NilError(t, linkForwarders(execPath, binDir))
	before, err := os.Stat(filepath.Join(binDir, "docker.exe"))
	assert.NilError(t, err)

	// A second run with the same bundle is a no-op for the hardlinks.
	assert.NilError(t, linkForwarders(execPath, binDir))
	after, err := os.Stat(filepath.Join(binDir, "docker.exe"))
	assert.NilError(t, err)
	assert.Assert(t, os.SameFile(before, after), "forwarder was needlessly replaced")

	// Changing the bundled forwarder refreshes the master and re-links tools.
	srcDir := filepath.Dir(execPath)
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, forwarderName), []byte("FORWARDER-v2"), 0o755))
	assert.NilError(t, linkForwarders(execPath, binDir))
	master, err := os.ReadFile(filepath.Join(binDir, forwarderName))
	assert.NilError(t, err)
	assert.Equal(t, string(master), "FORWARDER-v2")
	assertHardlink(t, filepath.Join(binDir, "docker.exe"), filepath.Join(binDir, forwarderName))

	// No aside files remain.
	for _, e := range readDirNames(t, binDir) {
		assert.Assert(t, filepath.Ext(e) != ".old", "leftover aside file %q", e)
	}
}

// TestLinkForwardersPreservesOtherFiles checks that the Windows bundle path
// reconciles in place: a file rdd did not write survives, since the directory
// holds the running rdd forwarder and cannot be wiped.
func TestLinkForwardersPreservesOtherFiles(t *testing.T) {
	execPath := bundleDir(t, "rdd.exe", "docker.exe")
	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0o755))
	foreign := filepath.Join(binDir, "user-notes.txt")
	assert.NilError(t, os.WriteFile(foreign, []byte("keep"), 0o644))

	assert.NilError(t, linkForwarders(execPath, binDir))

	data, err := os.ReadFile(foreign)
	assert.NilError(t, err, "unrelated file was removed")
	assert.Equal(t, string(data), "keep")
}

// TestLinkForwardersRemovesStale checks that a tool dropped from the bundle
// loses its forwarder and shim on the next run.
func TestLinkForwardersRemovesStale(t *testing.T) {
	execPath := bundleDir(t, "rdd.exe", "docker.exe", "helm.exe")
	srcDir := filepath.Dir(execPath)
	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, linkForwarders(execPath, binDir))

	// Drop helm from the bundle and re-run.
	assert.NilError(t, os.Remove(filepath.Join(srcDir, "helm.exe")))
	assert.NilError(t, linkForwarders(execPath, binDir))

	for _, name := range []string{"helm.exe", "helm.shim"} {
		_, err := os.Lstat(filepath.Join(binDir, name))
		assert.Assert(t, os.IsNotExist(err), "stale %q survived", name)
	}
	// docker survives.
	assertForwarder(t, binDir, "docker.exe", filepath.Join(srcDir, "docker.exe"))
}

func TestLinkForwardersMissingForwarder(t *testing.T) {
	srcDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, "rdd.exe"), []byte("binary"), 0o755))
	err := linkForwarders(filepath.Join(srcDir, "rdd.exe"), filepath.Join(t.TempDir(), "bin"))
	assert.ErrorContains(t, err, "locate forwarder")
}

// TestPruneDanglingForwarders checks the uninstall path: a forwarder whose shim
// target is gone is removed, while a live one and a shim-less file survive.
func TestPruneDanglingForwarders(t *testing.T) {
	binDir := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone.exe")
	live := filepath.Join(t.TempDir(), "live.exe")
	assert.NilError(t, os.WriteFile(live, []byte("binary"), 0o755))

	// A dangling forwarder (target removed by an uninstall).
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "docker.exe"), []byte("fwd"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "docker.shim"), shimContent(gone), 0o644))
	// A live forwarder.
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "helm.exe"), []byte("fwd"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "helm.shim"), shimContent(live), 0o644))
	// A plain file with no shim.
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "notes"), []byte("keep"), 0o644))

	pruneDanglingForwarders(binDir)

	for _, name := range []string{"docker.exe", "docker.shim"} {
		_, err := os.Lstat(filepath.Join(binDir, name))
		assert.Assert(t, os.IsNotExist(err), "dangling %q survived", name)
	}
	assertDirEntries(t, binDir, []string{"helm.exe", "helm.shim", "notes"})
}

func TestShimTarget(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"path = C:\\tools\\docker.exe\r\n": "C:\\tools\\docker.exe",
		"path=/usr/bin/rdd\n":              "/usr/bin/rdd",
		"args = foo\n":                     "",
	}
	for body, want := range cases {
		shim := filepath.Join(dir, "x.shim")
		assert.NilError(t, os.WriteFile(shim, []byte(body), 0o644))
		assert.Equal(t, shimTarget(shim), want)
	}
	assert.Equal(t, shimTarget(filepath.Join(dir, "missing.shim")), "")
}

func assertDirEntries(t *testing.T, dir string, want []string) {
	t.Helper()
	got := readDirNames(t, dir)
	slices.Sort(got)
	slices.Sort(want)
	assert.Assert(t, slices.Equal(got, want), "%q = %v, want %v", dir, got, want)
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
