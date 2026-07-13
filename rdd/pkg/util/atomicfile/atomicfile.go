// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package atomicfile updates file contents atomically and race-free: updates
// to the same file are serialized, and the new content is staged in a
// temporary file and renamed into place, so readers never see an empty or
// partially written file.
package atomicfile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// pathLocks holds one mutex per resolved target, so concurrent updates to
// the same file cannot lose each other's changes.
var pathLocks sync.Map

// Update replaces the contents of path with the result of modify, which
// receives the current contents (nil if the file does not exist) and returns
// the full new contents. Updates to the same file are serialized, and the
// write is skipped when modify returns the contents unchanged.
//
// Update follows symlinks and replaces the target, so a symlinked file (a
// dotfiles-managed kubeconfig or shell profile) keeps its link; a dangling
// link gets its target created. Update reports an error for a link it cannot
// resolve, such as a symlink loop or an unreadable parent directory. The
// temporary file lands in the target's directory, so the rename never crosses
// filesystems. An existing target keeps its mode and, where supported, its
// ownership; a new file gets perm.
//
// On Windows the rename fails with a sharing violation while another process
// holds the destination open (Go opens files without FILE_SHARE_DELETE);
// callers for whom this matters must retry.
func Update(path string, perm os.FileMode, modify func(current []byte) ([]byte, error)) error {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	mu := lockPath(target)
	mu.Lock()
	defer mu.Unlock()

	current, err := os.ReadFile(target)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		current = nil
	}
	updated, err := modify(current)
	if err != nil {
		return err
	}
	if bytes.Equal(current, updated) {
		return nil
	}
	return replace(target, updated, perm)
}

// Write replaces the contents of path with data, ignoring the current
// contents. Writing identical contents is a no-op. See Update for the
// serialization, symlink, and metadata behavior, which Write shares.
func Write(path string, data []byte, perm os.FileMode) error {
	target, err := resolveTarget(path)
	if err != nil {
		return err
	}
	mu := lockPath(target)
	mu.Lock()
	defer mu.Unlock()

	if current, err := os.ReadFile(target); err == nil && bytes.Equal(current, data) {
		return nil
	}
	return replace(target, data, perm)
}

// lockPath returns the mutex serializing updates of target.
func lockPath(target string) *sync.Mutex {
	mu, _ := pathLocks.LoadOrStore(target, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// replace writes data to a temporary file next to target and renames it into
// place. The caller holds the target's lock and has already resolved symlinks.
func replace(target string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	mode := perm
	if fi, statErr := os.Stat(target); statErr == nil && fi.Mode().IsRegular() {
		mode = fi.Mode()
		copyOwner(fi, tmpName)
	}
	if err = tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	return os.Rename(tmpName, target)
}

// resolveTarget follows symlinks to the file the write must replace. For a
// missing file or dangling link, EvalSymlinks fails with fs.ErrNotExist, and
// its PathError carries the path of the missing target. Any other failure —
// a symlink loop, an unreadable parent — leaves the target unknown, so
// resolveTarget reports it rather than let the caller replace the symlink.
func resolveTarget(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	var pathErr *fs.PathError
	if errors.Is(err, fs.ErrNotExist) && errors.As(err, &pathErr) {
		return pathErr.Path, nil
	}
	return "", fmt.Errorf("resolve %s: %w", path, err)
}
