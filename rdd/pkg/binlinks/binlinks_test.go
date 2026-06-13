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

func TestInAppBundle(t *testing.T) {
	cases := []struct {
		name     string
		execPath string
		goos     string
		want     bool
	}{
		{
			name:     "macOS app bundle",
			execPath: "/Applications/Rancher Desktop.app/Contents/Resources/darwin/bin/rdd",
			goos:     "darwin",
			want:     true,
		},
		{
			name:     "Linux app bundle",
			execPath: "/opt/rancher-desktop-2/resources/linux/bin/rdd",
			goos:     "linux",
			want:     true,
		},
		{
			name:     "Linux path with macOS casing",
			execPath: "/opt/rancher-desktop-2/Resources/linux/bin/rdd",
			goos:     "linux",
			want:     false,
		},
		{
			name:     "macOS path with Linux casing",
			execPath: "/Applications/Rancher Desktop.app/Contents/resources/darwin/bin/rdd",
			goos:     "darwin",
			want:     false,
		},
		{
			name:     "standalone CLI install",
			execPath: "/usr/local/bin/rdd",
			goos:     "darwin",
			want:     false,
		},
		{
			name:     "bundle path but goos mismatch",
			execPath: "/Applications/Rancher Desktop.app/Contents/Resources/darwin/bin/rdd",
			goos:     "linux",
			want:     false,
		},
		{
			name:     "unanchored suffix does not match",
			execPath: "/home/user/fooResources/darwin/bin/rdd",
			goos:     "darwin",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, inAppBundle(tc.execPath, tc.goos), tc.want)
		})
	}
}

func TestLinkBinaries(t *testing.T) {
	srcDir := t.TempDir()
	bundled := []string{"rdd", "docker", "helm"}
	for _, name := range bundled {
		assert.NilError(t, os.WriteFile(filepath.Join(srcDir, name), []byte("binary"), 0o755), "write %q", name)
	}
	// A subdirectory beside the executable must not be linked.
	assert.NilError(t, os.Mkdir(filepath.Join(srcDir, "subdir"), 0o755))
	execPath := filepath.Join(srcDir, "rdd")

	// Pre-populate binDir with a stale entry so the wipe can be verified.
	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "stale"), []byte("old"), 0o644))

	assert.NilError(t, linkBinaries(execPath, binDir))

	// The stale entry from the previous install is gone.
	_, err := os.Lstat(filepath.Join(binDir, "stale"))
	assert.Assert(t, os.IsNotExist(err), "stale entry survived the wipe: %v", err)

	// Every bundled file is linked to its source under the same name.
	for _, name := range bundled {
		assertSymlink(t, filepath.Join(binDir, name), filepath.Join(srcDir, name))
	}

	// kubectl is linked to the rdd executable.
	assertSymlink(t, filepath.Join(binDir, "kubectl"), execPath)

	// The subdirectory is not linked.
	_, err = os.Lstat(filepath.Join(binDir, "subdir"))
	assert.Assert(t, os.IsNotExist(err), "subdir was linked: %v", err)

	// Only the bundled files plus kubectl are present.
	entries, err := os.ReadDir(binDir)
	assert.NilError(t, err)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(got)
	want := []string{"docker", "helm", "kubectl", "rdd"}
	assert.Assert(t, slices.Equal(got, want), "binDir contents = %v, want %v", got, want)
}

// TestEnsureSelfLinks checks the standalone path: a missing or dangling rdd or
// kubectl link is repaired to point at the running executable, while a working
// link and any unrelated entry are left untouched.
func TestEnsureSelfLinks(t *testing.T) {
	srcDir := t.TempDir()
	execPath := filepath.Join(srcDir, "rdd")
	assert.NilError(t, os.WriteFile(execPath, []byte("binary"), 0o755))

	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0o755))

	// A working rdd link to a still-present binary must survive.
	appRdd := filepath.Join(srcDir, "app-rdd")
	assert.NilError(t, os.WriteFile(appRdd, []byte("binary"), 0o755))
	assert.NilError(t, os.Symlink(appRdd, filepath.Join(binDir, "rdd")))

	// A dangling kubectl link must be replaced.
	assert.NilError(t, os.Symlink(filepath.Join(srcDir, "gone"), filepath.Join(binDir, "kubectl")))

	// An unrelated entry must be left untouched.
	docker := filepath.Join(binDir, "docker")
	assert.NilError(t, os.Symlink(filepath.Join(srcDir, "docker"), docker))

	assert.NilError(t, ensureSelfLinks(execPath, binDir))

	// The working rdd link is preserved, still pointing at the app binary.
	assertSymlink(t, filepath.Join(binDir, "rdd"), appRdd)
	// The dangling kubectl link now points at the running executable.
	assertSymlink(t, filepath.Join(binDir, "kubectl"), execPath)
	// The unrelated link is left as it was.
	assertSymlink(t, docker, filepath.Join(srcDir, "docker"))
}

// TestEnsureSelfLinksCreatesDir checks that an instance with no bin directory —
// the app was never installed — gets one with rdd and kubectl linked to the
// running rdd.
func TestEnsureSelfLinksCreatesDir(t *testing.T) {
	srcDir := t.TempDir()
	execPath := filepath.Join(srcDir, "rdd")
	assert.NilError(t, os.WriteFile(execPath, []byte("binary"), 0o755))

	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, ensureSelfLinks(execPath, binDir))

	assertSymlink(t, filepath.Join(binDir, "rdd"), execPath)
	assertSymlink(t, filepath.Join(binDir, "kubectl"), execPath)
}

// assertSymlink fails unless path is a symlink that points at want.
func assertSymlink(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	assert.NilError(t, err, "lstat %q", path)
	assert.Assert(t, info.Mode()&os.ModeSymlink != 0, "%q is not a symlink", path)
	target, err := os.Readlink(path)
	assert.NilError(t, err, "readlink %q", path)
	assert.Equal(t, target, want)
}
