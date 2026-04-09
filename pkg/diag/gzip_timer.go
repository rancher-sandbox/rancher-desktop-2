// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package diag installs diagnostic instrumentation for investigating slow
// Lima image decompression on Windows CI runners. Specifically, it adds a
// logrus hook that intercepts Lima's "decompressing ..." and "Using cache"
// log messages and emits a structured "DIAG: lima decompression complete"
// entry with the elapsed time. This makes the gzip/xz subprocess duration
// directly grep-able from rdd.stderr.log instead of having to compute the
// gap between two unrelated log lines.
//
// This package is a temporary diagnostic. It is imported for side effects
// only. To remove it, delete the package and its `_` imports.
package diag

import (
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

func init() {
	logrus.AddHook(&decompressTimer{})
}

// decompressTimer matches Lima's downloader log messages to time the
// decompression subprocess invocation.
//
// Lima logs (in order, see lima/v2/pkg/downloader/downloader.go and
// lima/v2/pkg/fileutils/download.go):
//
//  1. "Attempting to download <description>"  (fileutils.DownloadFile)
//  2. "decompressing <ext> with <cmd>"        (downloader.decompressLocal, just before exec.Command.Run)
//  3. "Decompressing <description>\n"         (downloader.decompressLocal, only when !HideProgress)
//  4. <gzip subprocess runs>
//  5. "Using cache <path>"                    (fileutils.DownloadFile, after Download() returns)
//
// The interesting interval is between (2) and (5), which is the time spent
// in cmd.Run() plus a tiny amount of post-processing.
//
// We assume at most one decompression is in flight per process. The rdd
// controller serializes reconciles per VM, and the hostagent runs in a
// separate process — so within a single rdd.exe controller process, only
// one decompression should be active at any moment.
type decompressTimer struct {
	mu      sync.Mutex
	start   time.Time
	pending bool
}

func (t *decompressTimer) Levels() []logrus.Level {
	return []logrus.Level{logrus.InfoLevel}
}

func (t *decompressTimer) Fire(entry *logrus.Entry) error {
	switch {
	case strings.HasPrefix(entry.Message, "decompressing ."):
		t.mu.Lock()
		t.start = time.Now()
		t.pending = true
		t.mu.Unlock()
	case strings.HasPrefix(entry.Message, "Using cache "):
		t.mu.Lock()
		if !t.pending {
			t.mu.Unlock()
			return nil
		}
		elapsed := time.Since(t.start)
		t.pending = false
		t.mu.Unlock()
		// Re-emit through logrus. The hook will fire again on the new
		// entry, but neither prefix matches, so it short-circuits.
		logrus.WithFields(logrus.Fields{
			"diag":       "decompress-timer",
			"elapsed_ms": elapsed.Milliseconds(),
		}).Infof("DIAG: lima decompression complete in %s", elapsed)
	}
	return nil
}
