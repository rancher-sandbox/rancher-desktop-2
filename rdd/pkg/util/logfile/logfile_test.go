// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package logfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestCreateFirstFile(t *testing.T) {
	dir := t.TempDir()

	f, err := Create(dir, "test", false, "")
	assert.NilError(t, err)
	f.Close()

	// Should create test.log directly (no numbered file on first call)
	_, err = os.Stat(filepath.Join(dir, "test.log"))
	assert.NilError(t, err, "expected test.log to exist")
	_, err = os.Stat(filepath.Join(dir, "test.1.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no test.1.log on first call")
}

func TestSequentialNumbering(t *testing.T) {
	dir := t.TempDir()

	for i := 1; i <= 3; i++ {
		f, err := Create(dir, "app", true, "")
		assert.NilError(t, err, "Create #%d", i)
		f.Close()
	}

	// Active file should exist
	_, err := os.Stat(filepath.Join(dir, "app.log"))
	assert.NilError(t, err, "expected app.log to exist")

	// Two numbered backups should exist (from calls 2 and 3)
	for i := 1; i <= 2; i++ {
		name := filepath.Join(dir, fmt.Sprintf("app.%d.log", i))
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}

	// No app.3.log (the third call created app.log, not a numbered file)
	_, err = os.Stat(filepath.Join(dir, "app.3.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no app.3.log")
}

func TestPruning(t *testing.T) {
	dir := t.TempDir()

	// Create enough files to trigger pruning.
	// Call 1 creates prune.log (no rename). Subsequent calls rename to numbered files.
	count := retentionCount + 2
	for i := 1; i <= count; i++ {
		f, err := Create(dir, "prune", false, "")
		assert.NilError(t, err, "Create #%d", i)
		f.Close()
	}

	// File 1 should be pruned
	name := filepath.Join(dir, "prune.1.log")
	_, err := os.Stat(name)
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected %s to be pruned", name)

	// Remaining numbered files should still exist
	for n := 2; n <= count-1; n++ {
		name := filepath.Join(dir, fmt.Sprintf("prune.%d.log", n))
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}

	// Active file should exist
	_, err = os.Stat(filepath.Join(dir, "prune.log"))
	assert.NilError(t, err, "expected prune.log to exist")
}

func TestKeepAll(t *testing.T) {
	dir := t.TempDir()

	// Create enough files to exceed the retention count with keepAll=true.
	count := retentionCount + 2
	for i := 1; i <= count; i++ {
		f, err := Create(dir, "keep", true, "")
		assert.NilError(t, err, "Create #%d", i)
		f.Close()
	}

	// All numbered backups should exist
	for n := 1; n <= count-1; n++ {
		name := filepath.Join(dir, fmt.Sprintf("keep.%d.log", n))
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}

	// Active file should exist
	_, err := os.Stat(filepath.Join(dir, "keep.log"))
	assert.NilError(t, err, "expected keep.log to exist")
}

func TestHeader(t *testing.T) {
	dir := t.TempDir()

	header := "=== test header ===\n"
	f, err := Create(dir, "header", false, header)
	assert.NilError(t, err)
	f.Close()

	data, err := os.ReadFile(filepath.Join(dir, "header.log"))
	assert.NilError(t, err)
	assert.Equal(t, string(data), header)
}

func TestGapsInNumbering(t *testing.T) {
	dir := t.TempDir()

	// Manually create files with gaps: 1, 3, 5
	for _, n := range []int{1, 3, 5} {
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("gap.%d.log", n)))
		assert.NilError(t, err, "create gap file")
		f.Close()
	}

	// No gap.log exists, so nothing to rename. Active file is gap.log.
	f, err := Create(dir, "gap", false, "")
	assert.NilError(t, err)
	f.Close()

	_, err = os.Stat(filepath.Join(dir, "gap.log"))
	assert.NilError(t, err, "expected gap.log to exist")

	// No gap.6.log since there was nothing to rename
	_, err = os.Stat(filepath.Join(dir, "gap.6.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no gap.6.log")

	// Pre-existing files 1, 3, 5 should be pruned based on nextN=6
	_, err = os.Stat(filepath.Join(dir, "gap.1.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected gap.1.log to be pruned")
	for _, n := range []int{3, 5} {
		_, err = os.Stat(filepath.Join(dir, fmt.Sprintf("gap.%d.log", n)))
		assert.NilError(t, err, "expected gap.%d.log to exist", n)
	}
}

func TestCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")

	f, err := Create(dir, "test", false, "")
	assert.NilError(t, err)
	f.Close()

	_, err = os.Stat(filepath.Join(dir, "test.log"))
	assert.NilError(t, err, "expected test.log in nested dir")
}

func TestRenamePreservesContent(t *testing.T) {
	dir := t.TempDir()

	f, err := Create(dir, "app", false, "")
	assert.NilError(t, err)
	_, _ = f.WriteString("first log\n")
	f.Close()

	// Second call renames the first to app.1.log
	f, err = Create(dir, "app", false, "")
	assert.NilError(t, err)
	_, _ = f.WriteString("second log\n")
	f.Close()

	data, err := os.ReadFile(filepath.Join(dir, "app.1.log"))
	assert.NilError(t, err)
	assert.Equal(t, string(data), "first log\n")

	data, err = os.ReadFile(filepath.Join(dir, "app.log"))
	assert.NilError(t, err)
	assert.Equal(t, string(data), "second log\n")
}

func TestRotateNoFile(t *testing.T) {
	dir := t.TempDir()

	// Rotate with no existing file should be a no-op.
	err := Rotate(dir, "test", false)
	assert.NilError(t, err)

	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 0, "expected empty directory after rotating nonexistent file")
}

func TestRotateRenamesFile(t *testing.T) {
	dir := t.TempDir()

	// Create a file to rotate.
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "serial.log"), []byte("boot 1\n"), 0o644))

	err := Rotate(dir, "serial", false)
	assert.NilError(t, err)

	// Original should be gone, numbered backup should exist with original content.
	_, err = os.Stat(filepath.Join(dir, "serial.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected serial.log to be renamed")

	data, err := os.ReadFile(filepath.Join(dir, "serial.1.log"))
	assert.NilError(t, err)
	assert.Equal(t, string(data), "boot 1\n")
}

func TestRotateSequentialNumbering(t *testing.T) {
	dir := t.TempDir()

	for i := 1; i <= 3; i++ {
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "serial.log"), []byte(fmt.Sprintf("boot %d\n", i)), 0o644))
		assert.NilError(t, Rotate(dir, "serial", true))
	}

	// No active file should remain.
	_, err := os.Stat(filepath.Join(dir, "serial.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected serial.log to be renamed")

	// Three numbered backups should exist.
	for i := 1; i <= 3; i++ {
		data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("serial.%d.log", i)))
		assert.NilError(t, err, "expected serial.%d.log to exist", i)
		assert.Equal(t, string(data), fmt.Sprintf("boot %d\n", i))
	}
}

func TestRotatePruning(t *testing.T) {
	dir := t.TempDir()

	count := retentionCount + 2
	for i := 1; i <= count; i++ {
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "serial.log"), []byte(fmt.Sprintf("boot %d\n", i)), 0o644))
		assert.NilError(t, Rotate(dir, "serial", false))
	}

	// Oldest files should be pruned.
	for _, n := range []int{1, 2} {
		name := filepath.Join(dir, fmt.Sprintf("serial.%d.log", n))
		_, err := os.Stat(name)
		assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected %s to be pruned", name)
	}

	// Recent files should still exist.
	for n := 3; n <= count; n++ {
		name := filepath.Join(dir, fmt.Sprintf("serial.%d.log", n))
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}
}

func TestMultipleNamesInSameDir(t *testing.T) {
	dir := t.TempDir()

	// Create files with different names; they should not interfere
	f1, err := Create(dir, "stdout", false, "")
	assert.NilError(t, err)
	f1.Close()

	f2, err := Create(dir, "stderr", false, "")
	assert.NilError(t, err)
	f2.Close()

	// Both should have their own .log files
	_, err = os.Stat(filepath.Join(dir, "stdout.log"))
	assert.NilError(t, err, "expected stdout.log")
	_, err = os.Stat(filepath.Join(dir, "stderr.log"))
	assert.NilError(t, err, "expected stderr.log")
}
