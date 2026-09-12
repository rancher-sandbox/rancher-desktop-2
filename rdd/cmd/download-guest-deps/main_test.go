// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestdeps"
)

func TestStagedDepsPickTheImageFormatOfTheBackend(t *testing.T) {
	for goos, want := range map[string]string{
		"windows": "distro.tar.xz", // WSL2 imports a rootfs tarball
		"darwin":  "distro.raw.xz", // Lima's vz and qemu drivers boot a raw image
		"linux":   "distro.raw.xz",
	} {
		t.Run(goos, func(t *testing.T) {
			deps := stagedDeps(goos, "amd64")
			assert.Equal(t, len(deps), 1)
			assert.Equal(t, deps[0].filename, want)
			assert.Equal(t, deps[0].selector.Platform, "linux", "guest assets are Linux ones on every host")
			assert.Equal(t, deps[0].selector.Arch, "amd64")
		})
	}
}

// Every target the build supports must resolve against the manifest as
// checked in, so a rename or a dropped asset fails here rather than in a build.
func TestStagedDepsResolveAgainstTheManifest(t *testing.T) {
	manifest, err := guestdeps.LoadManifest(filepath.Join("..", "..", "dependencies.yaml"))
	assert.NilError(t, err)

	for _, goos := range []string{"darwin", "linux", "windows"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			t.Run(goos+"/"+goarch, func(t *testing.T) {
				for _, staged := range stagedDeps(goos, goarch) {
					dep, err := manifest.Select(staged.name, staged.selector)
					assert.NilError(t, err)
					assert.Assert(t, dep.Version != "")
				}
			})
		}
	}
}

// seedRun writes a manifest naming one arm64 asset and seeds the cache with its
// bytes, which keeps a run off the network. What these tests exercise is the
// wiring from manifest to staged file.
func seedRun(t *testing.T, body []byte) (manifestPath, cacheDir, destDir string) {
	t.Helper()
	sum := sha256.Sum256(body)
	dir := t.TempDir()

	manifestPath = filepath.Join(dir, "dependencies.yaml")
	manifest := fmt.Sprintf(`
distro:
  version: 0.2.7
  assets:
    - platform: linux
      arch: arm64
      variant: raw
      url: https://example.test/distro.v0.2.7.arm64.raw.xz
      checksum: sha256:%s
`, hex.EncodeToString(sum[:]))
	assert.NilError(t, os.WriteFile(manifestPath, []byte(manifest), 0o644))

	cacheDir = filepath.Join(dir, "cache")
	cachePath := filepath.Join(cacheDir, "distro", "v0.2.7", "distro.v0.2.7.arm64.raw.xz")
	assert.NilError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
	assert.NilError(t, os.WriteFile(cachePath, body, 0o644))

	return manifestPath, cacheDir, filepath.Join(dir, "embedded")
}

// run is what a build invokes. It reads the manifest, picks the asset for the
// target, and leaves it staged under the name the build expects.
func TestRunStagesTheAssetForTheTarget(t *testing.T) {
	body := []byte("distro image")
	manifestPath, cacheDir, destDir := seedRun(t, body)

	var log bytes.Buffer
	assert.NilError(t, run(t.Context(), &log, manifestPath, destDir, cacheDir, "linux", "arm64"))

	staged, err := os.ReadFile(filepath.Join(destDir, "distro.raw.xz"))
	assert.NilError(t, err)
	assert.Equal(t, string(staged), string(body))
	assert.Assert(t, strings.Contains(log.String(), "distro 0.2.7 is already downloaded to"),
		"the stager reports through run's log; it holds %q", log.String())
	assert.Assert(t, strings.Contains(log.String(), filepath.Join(destDir, "distro.raw.xz")),
		"the log names the staged file; it holds %q", log.String())
}

// run sweeps the cache once everything is staged, so a sweep that cannot read
// it warns instead of failing the build.
func TestRunWarnsWhenTheCacheCannotBePruned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not restrict listing on Windows")
	}
	body := []byte("distro image")
	manifestPath, cacheDir, destDir := seedRun(t, body)

	// Write and search stay open, so staging still reaches the entry by name;
	// only the listing Prune needs is refused.
	assert.NilError(t, os.Chmod(cacheDir, 0o300))
	t.Cleanup(func() { _ = os.Chmod(cacheDir, 0o755) })
	if _, err := os.ReadDir(cacheDir); err == nil {
		t.Skip("this user can list a directory with no read permission")
	}

	var log bytes.Buffer
	assert.NilError(t, run(t.Context(), &log, manifestPath, destDir, cacheDir, "linux", "arm64"))

	staged, err := os.ReadFile(filepath.Join(destDir, "distro.raw.xz"))
	assert.NilError(t, err)
	assert.Equal(t, string(staged), string(body))
	assert.Assert(t, strings.Contains(log.String(), "warning: pruning the download cache"),
		"the log records the failed sweep; it holds %q", log.String())
}

// A manifest naming an asset the target has no entry for fails with the
// manifest's own path, so the build says which file to fix.
func TestRunReportsAManifestMissingTheTarget(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "dependencies.yaml")
	assert.NilError(t, os.WriteFile(manifestPath, []byte(`
distro:
  version: 0.2.7
  assets:
    - platform: linux
      arch: amd64
      variant: raw
      url: https://example.test/distro.v0.2.7.amd64.raw.xz
      checksum: sha256:ac6c23589bc4a92a4c7d823d59b029576fa9bb18bc2c081e95f59fb184547795
`), 0o644))

	var log bytes.Buffer
	err := run(t.Context(), &log, manifestPath, filepath.Join(dir, "embedded"), filepath.Join(dir, "cache"), "linux", "arm64")
	assert.ErrorContains(t, err, manifestPath)
	assert.ErrorContains(t, err, "found 0")
}
