// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package gpu stages the AMD ROCm-on-WSL bridge files (librocdxg.so and
// dids.conf) onto the host so that Lima provisioning can install them into the
// guest's `/opt/rocm`. These two files ship only with a ROCm-on-WSL install and
// are not projected by WSL, present in the Windows driver store, or bundled in
// ROCm container images, so a privileged workload pod must bind-mount them from
// the guest.
//
// The files are obtained in one of two way:
//
//   - Fetched (default): download AMD's official, MIT-licensed rocdxg-roct
//     Debian package from the pinned GitHub release, verify its SHA-256, and
//     extract `librocdxg.so` + `dids.conf` from it.
//   - Source override: copy them from a caller-provided host directory (for
//     air-gapped hosts, or users who already have a ROCm-on-WSL install).
package gpu

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pinned release of https://github.com/ROCm/librocdxg. librocdxg must be
// ABI-compatible with the ROCm userspace in the workload image; v1.2.2 targets
// the ROCm 7.2.x series. Bump Version and DebSHA256 together when moving to a
// newer release.
//
// IMPORTANT: AMD driver must be present on the host, and the pinned librocdxg must be ABI-compatible with it.
//
// Currently pinned to the latest release of librocdxg.
const (
	Version = "1.2.2"

	DebURL = "https://github.com/ROCm/librocdxg/releases/download/v" + Version + "/rocdxg-roct_" + Version + "_amd64.deb"

	DebSHA256 = "28ded1254811192ebace1f76c0227580184af7b27ab2475fb9728295a702d541"

	// LibName is the library filename written into the staging directory and installed into the guest.
	LibName = "librocdxg.so"

	// DidsName is the supplemental PCI device-ID table filename.
	DidsName = "dids.conf"

	// versionMarker records which release populated the staging directory so
	// staging can be skipped when the files are already current.
	versionMarker = ".rocdxg-version"
)

var fetchTimeout = 2 * time.Minute

// Stage makes dir contain the current librocdxg.so and dids.conf.
//
// When source is non-empty the files are copied from that host directory;
// otherwise the pinned package is downloaded and its contents extracted. The
// operation is idempotent: if dir already holds both files and a version
// marker matching the pinned Version, Stage returns immediately without any
// network I/O.
func Stage(ctx context.Context, dir, source string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create GPU staging directory %s: %w", dir, err)
	}

	if source != "" {
		return stageFromSource(dir, source)
	}

	if staged(dir) {
		return nil
	}

	deb, err := fetchDeb(ctx)
	if err != nil {
		return err
	}
	lib, dids, err := extractFromDeb(deb)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, LibName), lib, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, DidsName), dids, 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, versionMarker), []byte(Version), 0o644)
}

// Unstage removes the staged GPU files from dir. It is safe to call when the
// files are not there.
func Unstage(dir string) error {
	for _, name := range []string{LibName, DidsName, versionMarker} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove %s: %w", name, err)
		}
	}
	return nil
}

// staged reports whether dir already holds both files at the pinned Version.
func staged(dir string) bool {
	marker, err := os.ReadFile(filepath.Join(dir, versionMarker))
	if err != nil || strings.TrimSpace(string(marker)) != Version {
		return false
	}
	for _, name := range []string{LibName, DidsName} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

// stageFromSource copies librocdxg.so (accepting a versioned name such as
// librocdxg.so.1.2.2) and dids.conf from source into dir.
func stageFromSource(dir, source string) error {
	lib, err := findSourceLib(source)
	if err != nil {
		return err
	}
	dids := filepath.Join(source, DidsName)
	if _, err := os.Stat(dids); err != nil {
		return fmt.Errorf("%s not found in %s: %w", DidsName, source, err)
	}

	libData, err := os.ReadFile(lib)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", lib, err)
	}
	didsData, err := os.ReadFile(dids)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", dids, err)
	}
	if err := writeFile(filepath.Join(dir, LibName), libData, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, DidsName), didsData, 0o644); err != nil {
		return err
	}
	// A source override does not correspond to a pinned release; clear any
	// marker so a later switch back to fetch mode re-downloads.
	if err := os.Remove(filepath.Join(dir, versionMarker)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to clear version marker: %w", err)
	}
	return nil
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

// fetchDeb downloads the pinned package and verifies its SHA-256.
func fetchDeb(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, DebURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for %s: %w", DebURL, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", DebURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download %s: status %s", DebURL, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", DebURL, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != DebSHA256 {
		return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", DebURL, got, DebSHA256)
	}
	return data, nil
}

// extractFromDeb pulls the librocdxg shared object and dids.conf out of a
// Debian package. A .deb is an ar archive whose data.tar.gz member holds the
// installed tree under ./opt/rocm.
func extractFromDeb(deb []byte) (lib, dids []byte, err error) {
	dataTarGz, err := arMember(deb, "data.tar.gz")
	if err != nil {
		return nil, nil, err
	}
	gz, err := gzip.NewReader(bytes.NewReader(dataTarGz))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open data.tar.gz: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read package data: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		switch {
		case lib == nil && strings.HasPrefix(base, LibName+"."):
			// The real object is versioned (librocdxg.so.1.2.2); the
			// unversioned names in the package are symlinks, which are
			// skipped by the TypeReg guard above.
			if lib, err = io.ReadAll(tr); err != nil {
				return nil, nil, fmt.Errorf("failed to read %s: %w", base, err)
			}
		case dids == nil && base == DidsName:
			if dids, err = io.ReadAll(tr); err != nil {
				return nil, nil, fmt.Errorf("failed to read %s: %w", base, err)
			}
		}
	}
	if lib == nil {
		return nil, nil, fmt.Errorf("%s not found in package", LibName)
	}
	if dids == nil {
		return nil, nil, fmt.Errorf("%s not found in package", DidsName)
	}
	return lib, dids, nil
}

// arMember returns the contents of the named member of a Unix ar archive. The
// format is a global magic followed by fixed 60-byte member headers; only the
// name and size fields are needed here.
func arMember(archive []byte, name string) ([]byte, error) {
	const (
		magic      = "!<arch>\n"
		headerSize = 60
		nameLen    = 16
		sizeOff    = 48
		sizeLen    = 10
	)
	if !bytes.HasPrefix(archive, []byte(magic)) {
		return nil, errors.New("not an ar archive")
	}
	off := len(magic)
	for off+headerSize <= len(archive) {
		hdr := archive[off : off+headerSize]
		// GNU ar terminates member names with "/" and pads with spaces.
		memberName := strings.TrimRight(strings.TrimSpace(string(hdr[:nameLen])), "/")
		size, err := strconv.Atoi(strings.TrimSpace(string(hdr[sizeOff : sizeOff+sizeLen])))
		if err != nil {
			return nil, fmt.Errorf("invalid ar member size: %w", err)
		}
		start := off + headerSize
		if start+size > len(archive) {
			return nil, fmt.Errorf("ar member %q truncated", memberName)
		}
		if memberName == name {
			return archive[start : start+size], nil
		}
		// Members are padded to an even offset.
		off = start + size
		if size%2 == 1 {
			off++
		}
	}
	return nil, fmt.Errorf("ar member %q not found", name)
}

// writeFile writes data to path with the given mode, truncating any existing
// file so a stale versioned artifact cannot linger.
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
