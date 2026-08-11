// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package gpu

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// buildDataTarGz produces a gzipped tar mirroring the layout of the real
// rocdxg-roct package: a versioned regular library, an unversioned symlink
// that must be ignored, and dids.conf.
func buildDataTarGz(t *testing.T, libContent, didsContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(hdr *tar.Header, body string) {
		assert.NilError(t, tw.WriteHeader(hdr))
		if body != "" {
			_, err := tw.Write([]byte(body))
			assert.NilError(t, err)
		}
	}

	write(&tar.Header{
		Name: "./opt/rocm/lib/librocdxg.so.1.2.2", Typeflag: tar.TypeReg,
		Mode: 0o644, Size: int64(len(libContent)),
	}, libContent)
	// An unversioned symlink that must be skipped in favour of the real object.
	write(&tar.Header{
		Name: "./opt/rocm/lib/librocdxg.so", Typeflag: tar.TypeSymlink,
		Linkname: "librocdxg.so.1.2.2", Mode: 0o777,
	}, "")
	write(&tar.Header{
		Name: "./opt/rocm/share/rocdxg/dids.conf", Typeflag: tar.TypeReg,
		Mode: 0o644, Size: int64(len(didsContent)),
	}, didsContent)

	assert.NilError(t, tw.Close())
	assert.NilError(t, gz.Close())
	return buf.Bytes()
}

// buildDeb wraps members in a minimal ar archive, matching the .deb layout.
func buildDeb(members map[string][]byte, order []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("!<arch>\n")
	for _, name := range order {
		data := members[name]
		header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8s%-10d`\n",
			name+"/", 0, 0, 0, "100644", len(data))
		buf.WriteString(header)
		buf.Write(data)
		if len(data)%2 == 1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes()
}

func TestExtractFromDeb(t *testing.T) {
	dataTarGz := buildDataTarGz(t, "REAL-LIB-CONTENT", "DIDS-CONTENT")
	deb := buildDeb(map[string][]byte{
		"debian-binary":  []byte("2.0\n"),
		"control.tar.gz": {0x1f, 0x8b, 0x08, 0x00}, // ignored gzip magic stub
		"data.tar.gz":    dataTarGz,
	}, []string{"debian-binary", "control.tar.gz", "data.tar.gz"})

	lib, dids, err := extractFromDeb(deb)
	assert.NilError(t, err)
	assert.Equal(t, string(lib), "REAL-LIB-CONTENT",
		"lib should be the versioned regular file content, not the symlink")
	assert.Equal(t, string(dids), "DIDS-CONTENT")
}

func TestArMemberPadding(t *testing.T) {
	// An odd-sized first member forces a pad byte before the next header.
	deb := buildDeb(map[string][]byte{
		"a":           []byte("odd"), // length 3 -> padded
		"data.tar.gz": []byte("payload"),
	}, []string{"a", "data.tar.gz"})

	got, err := arMember(deb, "data.tar.gz")
	assert.NilError(t, err)
	assert.Equal(t, string(got), "payload", "padding after an odd-sized member not handled")
}

func TestEnsureFromSource(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// Source has a versioned library name, as a real ROCm install does.
	assert.NilError(t, os.WriteFile(filepath.Join(src, "librocdxg.so.1.2.2"), []byte("LIB"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(src, DidsName), []byte("DIDS"), 0o644))

	assert.NilError(t, Stage(context.Background(), dst, src))

	gotLib, err := os.ReadFile(filepath.Join(dst, LibName))
	assert.NilError(t, err)
	assert.Equal(t, string(gotLib), "LIB", "versioned lib should be normalized to %s", LibName)
	gotDids, err := os.ReadFile(filepath.Join(dst, DidsName))
	assert.NilError(t, err)
	assert.Equal(t, string(gotDids), "DIDS")
}

func TestEnsureFromSourceMissingFiles(t *testing.T) {
	err := Stage(context.Background(), t.TempDir(), t.TempDir())
	assert.Assert(t, err != nil, "expected error when source lacks librocdxg.so and dids.conf")
}

func TestStagedAndUnstage(t *testing.T) {
	dir := t.TempDir()
	assert.Assert(t, !staged(dir), "empty dir should not be staged")

	// Simulate a completed fetch: both files plus a matching version marker.
	for name, content := range map[string]string{
		LibName: "x", DidsName: "y", versionMarker: Version,
	} {
		assert.NilError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	assert.Assert(t, staged(dir), "dir with both files and matching marker should be staged")

	// A version mismatch must invalidate the staging (so a bump re-fetches).
	assert.NilError(t, os.WriteFile(filepath.Join(dir, versionMarker), []byte("0.0.0"), 0o644))
	assert.Assert(t, !staged(dir), "mismatched version marker should not count as staged")

	assert.NilError(t, Unstage(dir))
	for _, name := range []string{LibName, DidsName, versionMarker} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.Assert(t, os.IsNotExist(err), "%s still present after Unstage", name)
	}
	// Unstage is idempotent.
	assert.NilError(t, Unstage(dir))
}
