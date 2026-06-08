// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestNextAvailableDir_FirstCall(t *testing.T) {
	parent := t.TempDir()
	dir, err := nextAvailableDir(parent, "instance")
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(dir), "instance")
	info, err := os.Stat(dir)
	assert.NilError(t, err)
	assert.Assert(t, info.IsDir())
}

func TestNextAvailableDir_Collision(t *testing.T) {
	parent := t.TempDir()

	dir1, err := nextAvailableDir(parent, "instance")
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(dir1), "instance")

	dir2, err := nextAvailableDir(parent, "instance")
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(dir2), "instance.2")

	dir3, err := nextAvailableDir(parent, "instance")
	assert.NilError(t, err)
	assert.Equal(t, filepath.Base(dir3), "instance.3")
}

func TestNextAvailableDir_ParentNotExist(t *testing.T) {
	_, err := nextAvailableDir(filepath.Join(t.TempDir(), "nonexistent"), "instance")
	assert.Assert(t, err != nil, "expected error for nonexistent parent")
}

func TestPreserveLogs_MovesLogFiles(t *testing.T) {
	origLogDir := LogDir
	logDir := t.TempDir()
	LogDir = func() string { return logDir }
	defer func() { LogDir = origLogDir }()

	srcDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, "serial.log"), []byte("serial output"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, "ha.stderr.log"), []byte("stderr output"), 0o644))
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, "lima.yaml"), []byte("not a log"), 0o644))
	assert.NilError(t, os.Mkdir(filepath.Join(srcDir, "nested.log"), 0o700))

	count, err := PreserveLogs(srcDir, "source")
	assert.NilError(t, err)
	assert.Equal(t, count, 2)

	// Verify files moved to destination.
	destDir := filepath.Join(logDir, "source")
	assertFileContent(t, filepath.Join(destDir, "serial.log"), "serial output")
	assertFileContent(t, filepath.Join(destDir, "ha.stderr.log"), "stderr output")

	// Verify non-log file was not moved.
	assertFileContent(t, filepath.Join(srcDir, "lima.yaml"), "not a log")

	// Verify source logs were removed (renamed away).
	_, err = os.Stat(filepath.Join(srcDir, "serial.log"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected serial.log to be removed from source")
}

func TestPreserveLogs_NoLogFiles(t *testing.T) {
	origLogDir := LogDir
	logDir := t.TempDir()
	LogDir = func() string { return logDir }
	defer func() { LogDir = origLogDir }()

	srcDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(srcDir, "lima.yaml"), []byte("config"), 0o644))

	count, err := PreserveLogs(srcDir, "empty")
	assert.NilError(t, err)
	assert.Equal(t, count, 0)

	// Verify no destination directory was created.
	_, err = os.Stat(filepath.Join(logDir, "empty"))
	assert.Assert(t, errors.Is(err, fs.ErrNotExist), "expected no destination directory for instance with no logs")
}

func TestPreserveLogs_SrcDirNotExist(t *testing.T) {
	_, err := PreserveLogs(filepath.Join(t.TempDir(), "nonexistent"), "instance")
	assert.Assert(t, err != nil, "expected error for nonexistent source directory")
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	assert.NilError(t, err, "failed to read %s", path)
	assert.Equal(t, string(data), expected)
}
