// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package logfile creates log files with automatic rotation.
//
// Each call to Create renames any existing {base}.log to {base}.{N}.log
// beside it and opens a fresh {base}.log. The active log always has a
// stable name.
package logfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const retentionCount = 5

type numberedFile struct {
	n    int
	name string
}

// Create opens a new log file at filePath, which must end in ".log",
// renaming any existing {base}.log to {base}.{N}.log first.
//
// When keepAll is false, old numbered files beyond the retention count
// are removed. If header is non-empty, it is written as the first line.
func Create(filePath string, keepAll bool, header string) (*os.File, error) {
	dir, _, err := splitLogPath(filePath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	if err := Rotate(filePath, keepAll); err != nil {
		return nil, err
	}

	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("create log file %s: %w", filePath, err)
	}

	if header != "" {
		if _, err := f.WriteString(header); err != nil {
			f.Close()
			os.Remove(filePath)
			return nil, fmt.Errorf("write header to %s: %w", filePath, err)
		}
	}

	return f, nil
}

// Rotate renames the log file at filePath, which must end in ".log", from
// {base}.log to {base}.{N}.log without creating a new file.
// Use this for logs managed by an external process (e.g., VM serial console
// output written by the VM driver) that would be overwritten on next start.
//
// When keepAll is false, old numbered files beyond the retention count
// are removed.
func Rotate(filePath string, keepAll bool) error {
	dir, name, err := splitLogPath(filePath)
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\.(\d+)\.log$`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read log directory %s: %w", dir, err)
	}

	// Find the highest existing sequence number.
	maxN := 0
	var numberedFiles []numberedFile
	for _, entry := range entries {
		matches := pattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			continue
		}
		n, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		numberedFiles = append(numberedFiles, numberedFile{n: n, name: entry.Name()})
		if n > maxN {
			maxN = n
		}
	}

	nextN := maxN + 1

	// Rename the current log to a numbered backup.
	if _, err := os.Lstat(filePath); err == nil {
		numberedName := fmt.Sprintf("%s.%d.log", name, nextN)
		if err := os.Rename(filePath, filepath.Join(dir, numberedName)); err != nil {
			return fmt.Errorf("rename %s to %s: %w", filePath, numberedName, err)
		}
		numberedFiles = append(numberedFiles, numberedFile{n: nextN, name: numberedName})
	}

	if !keepAll {
		pruneOldFiles(dir, numberedFiles, nextN)
	}

	return nil
}

// splitLogPath returns the directory of filePath and its file name without
// the ".log" suffix, or an error when filePath does not end in ".log".
func splitLogPath(filePath string) (dir, base string, err error) {
	base, ok := strings.CutSuffix(filepath.Base(filePath), ".log")
	if !ok {
		return "", "", fmt.Errorf("log file path %q does not end in .log", filePath)
	}
	return filepath.Dir(filePath), base, nil
}

// pruneOldFiles removes numbered log files beyond the retention count,
// keeping the most recent files (those with the highest sequence numbers).
func pruneOldFiles(dir string, files []numberedFile, currentN int) {
	// Keep files with sequence numbers > currentN - retentionCount.
	cutoff := currentN - retentionCount
	for _, nf := range files {
		if nf.n <= cutoff {
			_ = os.Remove(filepath.Join(dir, nf.name))
		}
	}
}
