// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package logfile

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"gotest.tools/v3/assert"
)

func TestCreateFirstFile(t *testing.T) {
	dir := t.TempDir()

	f, err := Create(dir, "test", false, "")
	assert.NilError(t, err)
	f.Close()

	// Should create test.1.log
	_, err = os.Stat(filepath.Join(dir, "test.1.log"))
	assert.NilError(t, err, "expected test.1.log to exist")

	// Should create symlink test.log -> test.1.log
	target, err := os.Readlink(filepath.Join(dir, "test.log"))
	assert.NilError(t, err)
	assert.Equal(t, target, "test.1.log")
}

func TestSequentialNumbering(t *testing.T) {
	dir := t.TempDir()

	for i := 1; i <= 3; i++ {
		f, err := Create(dir, "app", true, "")
		assert.NilError(t, err, "Create #%d", i)
		f.Close()
	}

	// All three files should exist
	for i := 1; i <= 3; i++ {
		name := filepath.Join(dir, "app."+itoa(i)+".log")
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}

	// Symlink should point to the latest
	target, err := os.Readlink(filepath.Join(dir, "app.log"))
	assert.NilError(t, err)
	assert.Equal(t, target, "app.3.log")
}

func TestPruning(t *testing.T) {
	dir := t.TempDir()

	// Create 7 files with pruning enabled
	for i := range 7 {
		f, err := Create(dir, "prune", false, "")
		assert.NilError(t, err, "Create #%d", i+1)
		f.Close()
	}

	// Files 1 and 2 should be pruned (7 - 5 = 2)
	for _, n := range []int{1, 2} {
		name := filepath.Join(dir, "prune."+itoa(n)+".log")
		_, err := os.Stat(name)
		assert.Assert(t, os.IsNotExist(err), "expected %s to be pruned", name)
	}

	// Files 3-7 should still exist
	for _, n := range []int{3, 4, 5, 6, 7} {
		name := filepath.Join(dir, "prune."+itoa(n)+".log")
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}
}

func TestKeepAll(t *testing.T) {
	dir := t.TempDir()

	// Create 7 files with keepAll=true
	for i := range 7 {
		f, err := Create(dir, "keep", true, "")
		assert.NilError(t, err, "Create #%d", i+1)
		f.Close()
	}

	// All files should exist
	for n := 1; n <= 7; n++ {
		name := filepath.Join(dir, "keep."+itoa(n)+".log")
		_, err := os.Stat(name)
		assert.NilError(t, err, "expected %s to exist", name)
	}
}

func TestHeader(t *testing.T) {
	dir := t.TempDir()

	header := "=== test header ===\n"
	f, err := Create(dir, "header", false, header)
	assert.NilError(t, err)
	f.Close()

	data, err := os.ReadFile(filepath.Join(dir, "header.1.log"))
	assert.NilError(t, err)
	assert.Equal(t, string(data), header)
}

func TestGapsInNumbering(t *testing.T) {
	dir := t.TempDir()

	// Manually create files with gaps: 1, 3, 5
	for _, n := range []int{1, 3, 5} {
		f, err := os.Create(filepath.Join(dir, "gap."+itoa(n)+".log"))
		assert.NilError(t, err, "create gap file")
		f.Close()
	}

	// Next file should be 6 (max=5, next=6)
	f, err := Create(dir, "gap", false, "")
	assert.NilError(t, err)
	f.Close()

	_, err = os.Stat(filepath.Join(dir, "gap.6.log"))
	assert.NilError(t, err, "expected gap.6.log to exist")

	target, err := os.Readlink(filepath.Join(dir, "gap.log"))
	assert.NilError(t, err)
	assert.Equal(t, target, "gap.6.log")
}

func TestCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")

	f, err := Create(dir, "test", false, "")
	assert.NilError(t, err)
	f.Close()

	_, err = os.Stat(filepath.Join(dir, "test.1.log"))
	assert.NilError(t, err, "expected test.1.log in nested dir")
}

func TestSymlinkFollowsTransparently(t *testing.T) {
	dir := t.TempDir()

	f, err := Create(dir, "follow", false, "")
	assert.NilError(t, err)
	_, _ = f.WriteString("content1\n")
	f.Close()

	// Reading via symlink should give the same content as the numbered file
	symlinkData, err := os.ReadFile(filepath.Join(dir, "follow.log"))
	assert.NilError(t, err)
	fileData, err := os.ReadFile(filepath.Join(dir, "follow.1.log"))
	assert.NilError(t, err)
	assert.Assert(t, bytes.Equal(symlinkData, fileData),
		"symlink content = %q, numbered file content = %q", symlinkData, fileData)
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

	// Both should have their own .1.log files
	_, err = os.Stat(filepath.Join(dir, "stdout.1.log"))
	assert.NilError(t, err, "expected stdout.1.log")
	_, err = os.Stat(filepath.Join(dir, "stderr.1.log"))
	assert.NilError(t, err, "expected stderr.1.log")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
