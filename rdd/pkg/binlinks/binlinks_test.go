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
		{
			name:     "Windows app bundle",
			execPath: `C:\Program Files\Rancher Desktop\resources\windows\bin\rdd.exe`,
			goos:     "windows",
			want:     true,
		},
		{
			name:     "Windows path without .exe suffix",
			execPath: `C:\Program Files\Rancher Desktop\resources\windows\bin\rdd`,
			goos:     "windows",
			want:     false,
		},
		{
			name:     "Windows path with forward slashes",
			execPath: "C:/Program Files/Rancher Desktop/resources/windows/bin/rdd.exe",
			goos:     "windows",
			want:     false,
		},
		{
			name:     "Windows standalone CLI install",
			execPath: `C:\Users\me\bin\rdd.exe`,
			goos:     "windows",
			want:     false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, inAppBundle(tc.execPath, tc.goos), tc.want)
		})
	}
}

// linkModes exercises both link kinds (symlink and the hardlink fallback) under
// both name conventions (bare, and the .exe suffix Windows uses).
var linkModes = []struct {
	name       string
	exe        string
	useSymlink bool
}{
	{"symlinks", "", true},
	{"hardlinks", "", false},
	{"symlinks with exe suffix", ".exe", true},
	{"hardlinks with exe suffix", ".exe", false},
}

func TestLinkBinaries(t *testing.T) {
	for _, tc := range linkModes {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := t.TempDir()
			bundled := []string{"rdd" + tc.exe, "docker" + tc.exe, "helm" + tc.exe}
			for _, name := range bundled {
				assert.NilError(t, os.WriteFile(filepath.Join(srcDir, name), []byte("binary"), 0o755), "write %q", name)
			}
			// A subdirectory beside the executable must not be linked.
			assert.NilError(t, os.Mkdir(filepath.Join(srcDir, "subdir"), 0o755))
			execPath := filepath.Join(srcDir, "rdd"+tc.exe)

			// Pre-populate binDir with a stale entry so the wipe can be verified.
			binDir := filepath.Join(t.TempDir(), "bin")
			assert.NilError(t, os.MkdirAll(binDir, 0o755))
			assert.NilError(t, os.WriteFile(filepath.Join(binDir, "stale"), []byte("old"), 0o644))

			assert.NilError(t, linkBinaries(execPath, binDir, tc.exe, tc.useSymlink))

			// The stale entry from the previous install is gone.
			_, err := os.Lstat(filepath.Join(binDir, "stale"))
			assert.Assert(t, os.IsNotExist(err), "stale entry survived the wipe: %v", err)

			// Every bundled file is linked to its source under the same name.
			for _, name := range bundled {
				assertLink(t, filepath.Join(binDir, name), filepath.Join(srcDir, name), tc.useSymlink)
			}

			// kubectl is linked to the rdd executable.
			assertLink(t, filepath.Join(binDir, "kubectl"+tc.exe), execPath, tc.useSymlink)

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
			want := append([]string{"kubectl" + tc.exe}, bundled...)
			slices.Sort(want)
			assert.Assert(t, slices.Equal(got, want), "binDir contents = %v, want %v", got, want)
		})
	}
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

	// An unrelated working link must be left untouched.
	dockerTarget := filepath.Join(srcDir, "docker")
	assert.NilError(t, os.WriteFile(dockerTarget, []byte("binary"), 0o755))
	docker := filepath.Join(binDir, "docker")
	assert.NilError(t, os.Symlink(dockerTarget, docker))

	assert.NilError(t, ensureSelfLinks(execPath, binDir, "", true))

	// The working rdd link is preserved, still pointing at the app binary.
	assertSymlink(t, filepath.Join(binDir, "rdd"), appRdd)
	// The dangling kubectl link now points at the running executable.
	assertSymlink(t, filepath.Join(binDir, "kubectl"), execPath)
	// The unrelated working link is left as it was.
	assertSymlink(t, docker, dockerTarget)
}

// TestEnsureSelfLinksCreatesDir checks that an instance with no bin directory —
// the app was never installed — gets one with rdd and kubectl linked to the
// running rdd.
func TestEnsureSelfLinksCreatesDir(t *testing.T) {
	for _, tc := range linkModes {
		t.Run(tc.name, func(t *testing.T) {
			srcDir := t.TempDir()
			execPath := filepath.Join(srcDir, "rdd"+tc.exe)
			assert.NilError(t, os.WriteFile(execPath, []byte("binary"), 0o755))

			binDir := filepath.Join(t.TempDir(), "bin")
			assert.NilError(t, ensureSelfLinks(execPath, binDir, tc.exe, tc.useSymlink))

			assertLink(t, filepath.Join(binDir, "rdd"+tc.exe), execPath, tc.useSymlink)
			assertLink(t, filepath.Join(binDir, "kubectl"+tc.exe), execPath, tc.useSymlink)
		})
	}
}

// TestEnsureSelfLinksPrunesDangling checks the uninstall path: a standalone rdd
// removes symlinks left dangling by a removed app so they cannot shadow a tool
// on PATH, while repairing its own rdd and kubectl links and leaving a working
// link and a plain file alone.
func TestEnsureSelfLinksPrunesDangling(t *testing.T) {
	srcDir := t.TempDir()
	execPath := filepath.Join(srcDir, "rdd")
	assert.NilError(t, os.WriteFile(execPath, []byte("binary"), 0o755))

	binDir := filepath.Join(t.TempDir(), "bin")
	assert.NilError(t, os.MkdirAll(binDir, 0o755))

	// A removed app leaves docker dangling, and rdd's own link dangling too.
	assert.NilError(t, os.Symlink(filepath.Join(srcDir, "gone"), filepath.Join(binDir, "docker")))
	assert.NilError(t, os.Symlink(filepath.Join(srcDir, "gone"), filepath.Join(binDir, "rdd")))
	// A working tool link and a plain file must survive.
	tool := filepath.Join(srcDir, "helm-real")
	assert.NilError(t, os.WriteFile(tool, []byte("binary"), 0o755))
	assert.NilError(t, os.Symlink(tool, filepath.Join(binDir, "helm")))
	assert.NilError(t, os.WriteFile(filepath.Join(binDir, "notes"), []byte("keep"), 0o644))

	assert.NilError(t, ensureSelfLinks(execPath, binDir, "", true))

	// The dangling docker link is pruned, not repaired: rdd does not provide it.
	_, err := os.Lstat(filepath.Join(binDir, "docker"))
	assert.Assert(t, os.IsNotExist(err), "dangling docker link survived: %v", err)
	// rdd and kubectl are repaired to the running standalone rdd.
	assertSymlink(t, filepath.Join(binDir, "rdd"), execPath)
	assertSymlink(t, filepath.Join(binDir, "kubectl"), execPath)
	// The working link and the plain file survive.
	assertSymlink(t, filepath.Join(binDir, "helm"), tool)
	_, err = os.Lstat(filepath.Join(binDir, "notes"))
	assert.NilError(t, err, "plain file was removed")
}

// assertLink fails unless path is the kind of link to want that link() creates
// for useSymlink: a symlink pointing at want, or a hardlink sharing its file.
func assertLink(t *testing.T, path, want string, useSymlink bool) {
	t.Helper()
	if useSymlink {
		assertSymlink(t, path, want)
		return
	}
	assertHardlink(t, path, want)
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

// assertHardlink fails unless path is a hardlink to want: not a symlink, and
// sharing want's file identity.
func assertHardlink(t *testing.T, path, want string) {
	t.Helper()
	link, err := os.Lstat(path)
	assert.NilError(t, err, "lstat %q", path)
	assert.Assert(t, link.Mode()&os.ModeSymlink == 0, "%q is a symlink, want a hardlink", path)
	pathInfo, err := os.Stat(path)
	assert.NilError(t, err, "stat %q", path)
	wantInfo, err := os.Stat(want)
	assert.NilError(t, err, "stat %q", want)
	assert.Assert(t, os.SameFile(pathInfo, wantInfo), "%q is not a hardlink to %q", path, want)
}
