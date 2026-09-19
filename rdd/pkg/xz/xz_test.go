// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package xz

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

// samplePlaintext is the content the tests compress and decode back. It is built
// from \n literals so the expectation does not depend on how git checks out line
// endings, and it is large enough to drive the decode loop over many reads.
func samplePlaintext() []byte {
	const block = "rancher-desktop-daemon in-process xz decoder fixture.\n" +
		"The quick brown fox jumps over the lazy dog.\n"
	return bytes.Repeat([]byte(block), 100_000)
}

// sparsePlaintext returns data, 4 MiB of zeros, more data, and 1 MiB of zeros,
// like a disk image with free space. Each data run ends partway into a 4 KiB
// block, and the trailing zeros check that the output keeps its full length
// and that finish punches their whole blocks.
func sparsePlaintext() []byte {
	data := samplePlaintext()[:10_000]
	return slices.Concat(data, make([]byte, 4<<20), data, make([]byte, 1<<20))
}

// xzCompress compresses data with the system xz CLI, so the tests exercise the
// decoder against real xz output rather than our own encoder. It skips when no
// xz is installed: the decoder runs without xz, but producing its test input
// needs one.
func xzCompress(t *testing.T, data []byte) []byte {
	t.Helper()
	if _, err := exec.LookPath("xz"); err != nil {
		t.Skip("xz not found in PATH")
	}
	cmd := exec.CommandContext(t.Context(), "xz", "--compress", "--stdout")
	cmd.Stdin = bytes.NewReader(data)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	assert.NilError(t, cmd.Run(), "xz failed: %s", stderr.String())
	return out.Bytes()
}

func TestDecompress(t *testing.T) {
	want := samplePlaintext()
	compressed := xzCompress(t, want)

	var got bytes.Buffer
	assert.NilError(t, Decompress(t.Context(), bytes.NewReader(compressed), &got))
	assert.Assert(t, bytes.Equal(got.Bytes(), want))
}

func TestDecompressTruncated(t *testing.T) {
	compressed := xzCompress(t, samplePlaintext())

	var got bytes.Buffer
	err := Decompress(t.Context(), bytes.NewReader(compressed[:len(compressed)/2]), &got)
	assert.Assert(t, err != nil, "truncated xz stream must fail")
}

func TestDecompressReader(t *testing.T) {
	dir := t.TempDir()
	want := samplePlaintext()

	dst := filepath.Join(dir, "image")
	assert.NilError(t, DecompressReader(t.Context(), bytes.NewReader(xzCompress(t, want)), dst))

	got, err := os.ReadFile(dst)
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(got, want))
	assertNoTempFiles(t, dir)
}

func TestDecompressReaderLeavesHoles(t *testing.T) {
	t.Skip("this branch decodes without punching holes, to measure what the holes cost the guest")
	want := sparsePlaintext()

	dst := filepath.Join(t.TempDir(), "image")
	assert.NilError(t, DecompressReader(t.Context(), bytes.NewReader(xzCompress(t, want)), dst))
	assertSparseCopy(t, dst, want)
}

func TestDecompressReaderLeavesNoPartialOutput(t *testing.T) {
	dir := t.TempDir()
	compressed := xzCompress(t, samplePlaintext())

	dst := filepath.Join(dir, "image")
	assert.Assert(t, DecompressReader(t.Context(), bytes.NewReader(compressed[:len(compressed)/2]), dst) != nil)

	_, err := os.Stat(dst)
	assert.Assert(t, os.IsNotExist(err), "dst must not exist after a failed decode")
	assertNoTempFiles(t, dir)
}

// A canceled context must abort the decode and leave neither a partial output
// nor a leftover temp file behind.
func TestDecompressReaderCanceledContext(t *testing.T) {
	dir := t.TempDir()
	compressed := xzCompress(t, samplePlaintext())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	dst := filepath.Join(dir, "image")
	err := DecompressReader(ctx, bytes.NewReader(compressed), dst)
	assert.Assert(t, errors.Is(err, context.Canceled), "want context.Canceled, got %v", err)

	_, statErr := os.Stat(dst)
	assert.Assert(t, os.IsNotExist(statErr), "dst must not exist after a canceled decode")
	assertNoTempFiles(t, dir)
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	assert.NilError(t, err)
	assert.Assert(t, len(matches) == 0, "leftover temp files: %v", matches)
}

// assertSparseCopy asserts that the file at path holds want and, where
// allocatedBytes can tell, that less than an eighth of it is allocated.
func assertSparseCopy(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(got, want), "got %d bytes, want %d", len(got), len(want))
	if allocated, ok := allocatedBytes(t, path); ok {
		assert.Assert(t, allocated < int64(len(want))/8, "%d of %d bytes allocated", allocated, len(want))
	}
}
