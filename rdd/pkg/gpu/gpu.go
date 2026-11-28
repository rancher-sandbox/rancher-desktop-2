// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package gpu prepares the AMD ROCm-on-WSL bridge library (librocdxg.so) so
// that Lima provisioning can install it into the guest's `/opt/rocm`.
//
// librocdxg is developed in https://github.com/ROCm/librocdxg. Its releases only
// ship Debian packages, and Rancher Desktop must not redistribute binaries, so
// when the user opts in to GPU passthrough the library is built from the
// tagged source on their machine:
//
//   - Host side (this package): download the pinned build inputs, verify their
//     SHA-256, and stage them with the build script under dir/src.
//   - Guest side (lima-template.yaml): run build.sh in a throwaway container
//     that writes dir/librocdxg.so, then install it into /opt/rocm/lib.
//
// The inputs are fetched from AMD's and Microsoft's public locations and are
// only ever downloaded onto the machine of a user who enabled the feature.
//
// Alternatively, spec.gpu.source names a host directory that already holds a
// librocdxg.so, which is copied instead of building (air-gapped hosts, or users
// with an existing ROCm-on-WSL install).
package gpu

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// ReleaseTag is the ROCm/librocdxg release whose source is built. It must
	// be a released tag: builds of other revisions (for example the copy in the
	// rocm-systems monorepo) are not ABI-compatible with the ROCm runtime in
	// the workload images. Bump it together with the checksums in inputs.
	ReleaseTag = "v1.2.2"

	// BuildID identifies the librocdxg that is produced. Bump the suffix when
	// build.sh or the container image changes without a ReleaseTag change.
	BuildID = ReleaseTag + "-r2"

	// BuildImage is the container image that build.sh runs in, pinned by digest.
	BuildImage = "docker.io/library/ubuntu@sha256:f64b2ffc2f10ab8f8505aa56b2664bd846cbb9df87c7e2532e30f7667021c0e6"

	// LibName is the library filename written into the staging directory and
	// installed into the guest.
	LibName = "librocdxg.so"

	// SrcDir is the staging subdirectory holding the build inputs.
	SrcDir = "src"

	// BuildScriptName is the script run inside the build container.
	BuildScriptName = "build.sh"

	// buildIDMarker records which BuildID produced LibName (or sourceMarker when
	// it was copied from a user-supplied directory).
	buildIDMarker = ".build-id"

	// sourceMarker is written to buildIDMarker by a source override.
	sourceMarker = "source"

	// stagedIDFile is the file under SrcDir that holds the BuildID of the staged
	// inputs; the guest copies it to buildIDMarker after a successful build.
	stagedIDFile = "build-id"

	// imageFile is the file under SrcDir that holds BuildImage for the guest.
	imageFile = "image"
)

const (
	sourceURL = "https://github.com/ROCm/librocdxg/archive/refs/tags/" + ReleaseTag + ".tar.gz"
	// Pinned commit of microsoft/win32metadata, which publishes the Windows
	// headers that librocdxg expects from a Windows SDK.
	winSDKBase = "https://raw.githubusercontent.com/microsoft/win32metadata/a40ed3186943620f304649a077b78f8271034736/generation/WinSDK/RecompiledIdlHeaders/shared/"
)

// input is one file the build needs, relative to the SrcDir.
type input struct {
	path   string
	url    string
	sha256 string
}

// inputs lists everything build.sh consumes. It is a var so tests can point it
// at a local server.
var inputs = []input{
	{"librocdxg.tar.gz", sourceURL, "12f1eb13948ea80ebbaf4d0380184291782a1c481e3d7296dfe7ff3e80f3d71b"},
	{"winsdk/d3dkmdt.h", winSDKBase + "d3dkmdt.h", "d8f0ec92fce79ffa208da7b4bb01b5cc901e0aba26acf93abb4be6199aad8472"},
	{"winsdk/d3dkmthk.h", winSDKBase + "d3dkmthk.h", "0a63e5fc21446a923512108822572e6e178befda58929e8b1777ee50580cee8b"},
	{"winsdk/d3dukmdt.h", winSDKBase + "d3dukmdt.h", "dbac0fba615da4f7e910d7dbadcd8ba03832f0a820ea660a0f5b86a192ce89da"},
	{"winsdk/guiddef.h", winSDKBase + "guiddef.h", "7b0ff3b03ace4b2b26f2765ff7b68a925242ee13f678672491c4b347b0dcbff6"},
	{"winsdk/no_sal2.h", winSDKBase + "no_sal2.h", "2da71d7f74704ae9074a1f5b6d68d5c69f3896c8d8bdae4e05e7590adbc61ae4"},
	{"winsdk/ntstatus.h", winSDKBase + "ntstatus.h", "457c375cd907d235d4f036ed9262ff9f245becf55c819bb3d1d1323c03d0195f"},
	{"winsdk/winapifamily.h", winSDKBase + "winapifamily.h", "23c2e6b0b10fd2948d230c8e9fd4cb15fa9bb7bbb4ee1808cf07f06f6d4298fc"},
	{"winsdk/winpackagefamily.h", winSDKBase + "winpackagefamily.h", "d7d624a09b573310f2ff8c6439acc154c79e706b4c3f2f382334d3f52a254b4d"},
}

//go:embed build.sh
var buildScript []byte

var fetchTimeout = 2 * time.Minute

// Stage makes dir ready for the guest to install librocdxg.so.
//
// When source is non-empty the library is copied from that host directory.
// Otherwise the build inputs are downloaded into dir/src for the guest to
// build; a library left over from a different BuildID is removed so the guest
// rebuilds it. The operation is idempotent and does no network I/O when the
// inputs, or an up-to-date library, are already in place.
func Stage(ctx context.Context, dir, source string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create GPU staging directory %s: %w", dir, err)
	}

	if source != "" {
		return stageFromSource(dir, source)
	}

	if built(dir) {
		return nil
	}
	// The library is stale (older build, or a source override that was since
	// unset); drop it so the guest rebuilds.
	if err := removeAll(filepath.Join(dir, LibName), filepath.Join(dir, buildIDMarker)); err != nil {
		return err
	}

	src := filepath.Join(dir, SrcDir)
	for _, in := range inputs {
		if err := fetch(ctx, src, in); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(src, BuildScriptName), bytes.ReplaceAll(buildScript, []byte("\r\n"), []byte("\n")), 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(src, imageFile), []byte(BuildImage), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(src, stagedIDFile), []byte(BuildID), 0o644)
}

// Unstage removes everything Stage created in dir. It is safe to call when
// nothing is staged.
func Unstage(dir string) error {
	if err := removeAll(filepath.Join(dir, LibName), filepath.Join(dir, buildIDMarker)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, SrcDir)); err != nil {
		return fmt.Errorf("failed to remove %s: %w", SrcDir, err)
	}
	return nil
}

// built reports whether dir already holds a librocdxg.so built for BuildID.
func built(dir string) bool {
	marker, err := os.ReadFile(filepath.Join(dir, buildIDMarker))
	if err != nil || strings.TrimSpace(string(marker)) != BuildID {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, LibName))
	return err == nil
}

// stageFromSource copies librocdxg.so (accepting a versioned name such as
// librocdxg.so.1.2.2) from source into dir.
func stageFromSource(dir, source string) error {
	lib, err := findSourceLib(source)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(lib)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", lib, err)
	}
	if err := writeFile(filepath.Join(dir, LibName), data, 0o755); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, buildIDMarker), []byte(sourceMarker), 0o644)
}

// findSourceLib returns the path to librocdxg.so in source, accepting a
// versioned name (librocdxg.so.1.2.2) when the unversioned file is absent. The
// highest-sorting candidate wins for determinism.
func findSourceLib(source string) (string, error) {
	exact := filepath.Join(source, LibName)
	if _, err := os.Stat(exact); err == nil {
		return exact, nil
	}
	matches, err := filepath.Glob(filepath.Join(source, LibName+".*"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%s not found in %s", LibName, source)
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

// fetch downloads in into src unless a file with the expected checksum is
// already there. The checksum is verified before the file is put in place.
func fetch(ctx context.Context, src string, in input) error {
	dest := filepath.Join(src, filepath.FromSlash(in.path))
	if data, err := os.ReadFile(dest); err == nil && checksum(data) == in.sha256 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.url, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to build request for %s: %w", in.url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", in.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: status %s", in.url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", in.url, err)
	}
	if got := checksum(data); got != in.sha256 {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", in.url, got, in.sha256)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(dest), err)
	}
	return writeFile(dest, data, 0o644)
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func removeAll(paths ...string) error {
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", p, err)
		}
	}
	return nil
}

// writeFile writes data to path with the given mode, truncating any existing
// file so a stale artifact cannot linger.
func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	// WriteFile only applies mode on creation; enforce it on overwrite too.
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("failed to set mode on %s: %w", path, err)
	}
	return nil
}
