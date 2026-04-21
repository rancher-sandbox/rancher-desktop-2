// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

// Package watch provides the FileWatcher abstraction used by
// pkg/util/tail to observe changes to a file.
package watch

import (
	"sync"

	"gopkg.in/tomb.v1"
)

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the file comes into existence.
	BlockUntilExists(*tomb.Tomb) error

	// ChangeEvents reports on changes to a file, be it modification,
	// deletion, renames or truncations. After a deletion or rename event
	// the implementation's goroutine returns and no further notifications
	// arrive on the returned FileChanges; the channels themselves stay
	// open. Modification and truncation events are delivered indefinitely.
	// In order to properly report truncations, ChangeEvents requires
	// the caller to pass their current offset in the file.
	//
	// wg tracks the background goroutine spawned by the implementation.
	// The caller must wait on wg after the tomb goes Dying to ensure any
	// per-watcher cleanup (e.g. untrack on the shared InotifyTracker)
	// runs before the caller starts another watch on the same filename.
	ChangeEvents(*tomb.Tomb, int64, *sync.WaitGroup) (*FileChanges, error)
}
