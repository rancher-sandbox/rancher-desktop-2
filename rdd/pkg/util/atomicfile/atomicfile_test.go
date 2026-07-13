// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// mkSymlink creates a symlink, skipping the test on Windows where symlink
// creation needs elevated privileges or developer mode.
func mkSymlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	if err != nil && runtime.GOOS == "windows" {
		t.Skipf("cannot create symlink: %v", err)
	}
	assert.NilError(t, err)
}

// mkUnreachableSymlink returns a link to a file inside a directory the caller
// cannot traverse.
func mkUnreachableSymlink(t *testing.T, dir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not block traversal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root traverses directories regardless of their mode")
	}
	secret := filepath.Join(dir, "secret")
	assert.NilError(t, os.Mkdir(secret, 0o700))
	target := filepath.Join(secret, "real-config")
	assert.NilError(t, os.WriteFile(target, []byte("real"), 0o600))
	link := filepath.Join(dir, "link-config")
	mkSymlink(t, target, link)
	assert.NilError(t, os.Chmod(secret, 0o000))
	t.Cleanup(func() { _ = os.Chmod(secret, 0o700) })
	return link
}

// eachWriter runs fn against both entry points to test behavior they share.
func eachWriter(t *testing.T, fn func(t *testing.T, write func(path string) error)) {
	t.Helper()
	t.Run("Write", func(t *testing.T) {
		fn(t, func(path string) error {
			return Write(path, []byte("content"), 0o600)
		})
	})
	t.Run("Update", func(t *testing.T) {
		fn(t, func(path string) error {
			return Update(path, 0o600, func([]byte) ([]byte, error) {
				return []byte("content"), nil
			})
		})
	})
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, fi.Mode().Perm(), want)
}

func TestWriteCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")

	assert.NilError(t, Write(path, []byte("content"), 0o600))

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "content")
	assertMode(t, path, 0o600)
}

func TestWriteReplacesContentKeepingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte("old"), 0o644))

	assert.NilError(t, Write(path, []byte("new"), 0o600))

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "new")
	// The existing file's mode wins over the perm argument.
	assertMode(t, path, 0o644)
}

func TestWriteSkipsIdenticalContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, Write(path, []byte("content"), 0o600))
	info1, err := os.Stat(path)
	assert.NilError(t, err)

	assert.NilError(t, Write(path, []byte("content"), 0o600))

	info2, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, info1.ModTime(), info2.ModTime())
}

func TestUpdateCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")

	err := Update(path, 0o600, func(current []byte) ([]byte, error) {
		assert.Assert(t, current == nil, "missing file must present nil contents")
		return []byte("content"), nil
	})
	assert.NilError(t, err)

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "content")
}

func TestUpdateModifiesExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte("old"), 0o600))

	err := Update(path, 0o600, func(current []byte) ([]byte, error) {
		return append(current, []byte("+new")...), nil
	})
	assert.NilError(t, err)

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "old+new")
}

func TestUpdateSkipsUnchangedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte("content"), 0o600))
	info1, err := os.Stat(path)
	assert.NilError(t, err)

	err = Update(path, 0o600, func(current []byte) ([]byte, error) {
		return current, nil
	})
	assert.NilError(t, err)

	info2, err := os.Stat(path)
	assert.NilError(t, err)
	assert.Equal(t, info1.ModTime(), info2.ModTime())
}

func TestUpdatePropagatesModifyError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte("content"), 0o600))

	err := Update(path, 0o600, func([]byte) ([]byte, error) {
		return nil, errors.New("modify failed")
	})
	assert.Error(t, err, "modify failed")

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "content")
}

func TestUpdatesSerialized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte("start"), 0o600))

	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- Update(path, 0o600, func([]byte) ([]byte, error) {
			close(entered)
			<-release
			return []byte("first"), nil
		})
	}()
	<-entered

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- Update(path, 0o600, func([]byte) ([]byte, error) {
			return []byte("second"), nil
		})
	}()

	select {
	case <-secondDone:
		assert.Assert(t, false, "second update ran inside the first update's critical section")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	assert.NilError(t, <-firstDone)
	assert.NilError(t, <-secondDone)

	data, err := os.ReadFile(path)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "second")
}

func TestWritePreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-config")
	link := filepath.Join(dir, "link-config")
	assert.NilError(t, os.WriteFile(target, []byte("old"), 0o600))
	mkSymlink(t, target, link)

	assert.NilError(t, Write(link, []byte("new"), 0o600))

	fi, err := os.Lstat(link)
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink != 0, "link was replaced by a regular file")
	data, err := os.ReadFile(target)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "new")
}

func TestWritePreservesRelativeSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-config")
	link := filepath.Join(dir, "link-config")
	assert.NilError(t, os.WriteFile(target, []byte("old"), 0o600))
	mkSymlink(t, "real-config", link)

	assert.NilError(t, Write(link, []byte("new"), 0o600))

	fi, err := os.Lstat(link)
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink != 0, "link was replaced by a regular file")
	data, err := os.ReadFile(target)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "new")
}

func TestWriteCreatesDanglingSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "missing-config")
	link := filepath.Join(dir, "link-config")
	mkSymlink(t, target, link)

	assert.NilError(t, Write(link, []byte("content"), 0o600))

	fi, err := os.Lstat(link)
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink != 0, "link was replaced by a regular file")
	data, err := os.ReadFile(target)
	assert.NilError(t, err)
	assert.Equal(t, string(data), "content")
}

func TestSymlinkLoopIsRefused(t *testing.T) {
	eachWriter(t, func(t *testing.T, write func(string) error) {
		dir := t.TempDir()
		first := filepath.Join(dir, "first")
		second := filepath.Join(dir, "second")
		mkSymlink(t, second, first)
		mkSymlink(t, first, second)

		assert.ErrorContains(t, write(first), "resolve")

		fi, err := os.Lstat(first)
		assert.NilError(t, err)
		assert.Assert(t, fi.Mode()&os.ModeSymlink != 0, "link was replaced by a regular file")
	})
}

func TestUnreachableSymlinkTargetIsRefused(t *testing.T) {
	eachWriter(t, func(t *testing.T, write func(string) error) {
		link := mkUnreachableSymlink(t, t.TempDir())

		err := write(link)
		assert.ErrorContains(t, err, "resolve")
		assert.Assert(t, errors.Is(err, fs.ErrPermission))

		fi, err := os.Lstat(link)
		assert.NilError(t, err)
		assert.Assert(t, fi.Mode()&os.ModeSymlink != 0, "link was replaced by a regular file")
	})
}

func TestWriteLeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	assert.NilError(t, Write(path, []byte("content"), 0o600))

	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 1)
	assert.Equal(t, entries[0].Name(), "config")
}

func TestWriteNeverExposesPartialFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Go opens files without FILE_SHARE_DELETE, so the reader's open
		// handle would make the writer's rename fail with a sharing violation.
		t.Skip("rename over a concurrently open file is not atomic on Windows")
	}
	path := filepath.Join(t.TempDir(), "config")
	long := bytes.Repeat([]byte("a"), 4096)
	short := bytes.Repeat([]byte("b"), 64)
	assert.NilError(t, Write(path, long, 0o600))

	stop := make(chan struct{})
	bad := make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			switch {
			case err != nil:
				bad <- fmt.Sprintf("read failed: %v", err)
				return
			case !bytes.Equal(data, long) && !bytes.Equal(data, short):
				bad <- fmt.Sprintf("partial content: %d bytes", len(data))
				return
			}
		}
	}()

	for i := range 500 {
		content := long
		if i%2 == 0 {
			content = short
		}
		assert.NilError(t, Write(path, content, 0o600))
	}
	close(stop)
	wg.Wait()
	close(bad)
	assert.Equal(t, <-bad, "")
}
