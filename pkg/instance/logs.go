// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PreserveLogs moves .log files from srcDir into a uniquely named
// subdirectory of LogDir(). The subdirectory uses instanceName as a
// base, with a numeric suffix to avoid collisions (.2, .3, etc.).
//
// Returns the number of files moved. Skips srcDir entries that are
// directories or do not end in ".log".
func PreserveLogs(srcDir, instanceName string) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, fmt.Errorf("read instance directory %s: %w", srcDir, err)
	}

	// Filter to .log files before creating the destination directory,
	// so we don't leave empty directories when no logs exist.
	// Symlinked log files are not expected and not supported; they
	// would become dangling after the source directory is removed.
	var logFiles []os.DirEntry
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".log") {
			logFiles = append(logFiles, entry)
		}
	}
	if len(logFiles) == 0 {
		return 0, nil
	}

	logDir := LogDir()
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return 0, fmt.Errorf("create log directory %s: %w", logDir, err)
	}

	destDir, err := nextAvailableDir(logDir, instanceName)
	if err != nil {
		return 0, err
	}

	var errs []error
	count := 0
	for _, entry := range logFiles {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(destDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			errs = append(errs, fmt.Errorf("preserve %s: %w", entry.Name(), err))
			continue
		}
		count++
	}
	if count == 0 {
		os.Remove(destDir)
	}

	return count, errors.Join(errs...)
}

// nextAvailableDir creates a directory named {name} under parent. If it
// already exists, it tries {name}.2, {name}.3, etc., up to {name}.1000.
func nextAvailableDir(parent, name string) (string, error) {
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o700); err == nil {
		return dir, nil
	} else if !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("create directory %s: %w", dir, err)
	}
	for n := 2; n <= 1000; n++ {
		dir = filepath.Join(parent, fmt.Sprintf("%s.%d", name, n))
		if err := os.Mkdir(dir, 0o700); err == nil {
			return dir, nil
		} else if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create directory %s: %w", dir, err)
		}
	}
	return "", fmt.Errorf("exhausted directory names for %s in %s", name, parent)
}
