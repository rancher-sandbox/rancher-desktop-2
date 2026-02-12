// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package logfile creates sequentially numbered log files with symlinks.
//
// Each call to Create opens the next numbered file ({name}.{N}.log) and
// updates a symlink ({name}.log) pointing to it. Callers that open the
// symlink (e.g., tail -f, Lima's StopGracefully) follow it transparently.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

const retentionCount = 5

type numberedFile struct {
	n    int
	name string
}

// Create opens the next numbered log file in dir and updates the symlink.
//
// File naming: {name}.{N}.log, with symlink {name}.log -> {name}.{N}.log.
// When keepAll is false, numbered files beyond the retention count are removed.
// If header is non-empty, it is written as the first line of the new file.
func Create(dir, name string, keepAll bool, header string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create log directory %s: %w", dir, err)
	}

	pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `\.(\d+)\.log$`)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read log directory %s: %w", dir, err)
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
	fileName := fmt.Sprintf("%s.%d.log", name, nextN)
	filePath := filepath.Join(dir, fileName)

	f, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("create log file %s: %w", filePath, err)
	}

	if header != "" {
		if _, err := f.WriteString(header); err != nil {
			f.Close()
			return nil, fmt.Errorf("write header to %s: %w", filePath, err)
		}
	}

	// Update symlink: remove old one first (Symlink fails if target exists).
	symlinkPath := filepath.Join(dir, name+".log")
	_ = os.Remove(symlinkPath)
	if err := os.Symlink(fileName, symlinkPath); err != nil {
		// Non-fatal: the file is still usable, just without the symlink.
		fmt.Fprintf(os.Stderr, "logfile: failed to create symlink %s -> %s: %v\n", symlinkPath, fileName, err)
	}

	if !keepAll {
		pruneOldFiles(dir, numberedFiles, nextN)
	}

	return f, nil
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
