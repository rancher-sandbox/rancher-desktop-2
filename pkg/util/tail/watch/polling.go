// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

package watch

import (
	"os"
	"sync"
	"time"

	"gopkg.in/tomb.v1"
)

// PollingFileWatcher polls the file for changes.
type PollingFileWatcher struct {
	Filename string
	Size     int64
}

// NewPollingFileWatcher returns a FileWatcher that polls the given
// filename instead of using a kernel notification mechanism.
func NewPollingFileWatcher(filename string) *PollingFileWatcher {
	fw := &PollingFileWatcher{filename, 0}
	return fw
}

// pollDuration is the interval at which a PollingFileWatcher re-stats
// the file it watches.
const pollDuration = 250 * time.Millisecond

// BlockUntilExists blocks until the file appears or the tomb is dying.
func (fw *PollingFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	for {
		if _, err := os.Stat(fw.Filename); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		select {
		case <-time.After(pollDuration):
			continue
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
}

// ChangeEvents returns a FileChanges whose channels fire whenever the
// watched file is modified, truncated, or deleted. The caller passes
// the current read offset so truncation can be detected. wg is
// incremented before the goroutine is spawned and Done when the
// goroutine exits so callers can synchronise teardown.
func (fw *PollingFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64, wg *sync.WaitGroup) (*FileChanges, error) {
	origFi, err := os.Stat(fw.Filename)
	if err != nil {
		return nil, err
	}

	changes := NewFileChanges()
	var prevModTime time.Time

	// XXX: use tomb.Tomb to cleanly manage these goroutines. replace
	// the fatal (below) with tomb's Kill.

	fw.Size = pos

	wg.Add(1)
	go func() {
		defer wg.Done()
		prevSize := fw.Size
		for {
			select {
			case <-t.Dying():
				return
			case <-time.After(pollDuration):
			}

			fi, err := os.Stat(fw.Filename)
			if err != nil {
				// Treat any stat failure (IsNotExist, permission — Windows
				// keeps a handle open while the tail holds the file —
				// transient I/O) as a deletion: the tail reopens on the
				// next cycle or exits cleanly. Panicking would crash the
				// host process, because this runs in a detached goroutine
				// the caller cannot recover from.
				changes.NotifyDeleted()
				return
			}

			// File got moved/renamed?
			if !os.SameFile(origFi, fi) {
				changes.NotifyDeleted()
				return
			}

			// File got truncated?
			fw.Size = fi.Size()
			if prevSize > 0 && prevSize > fw.Size {
				changes.NotifyTruncated()
				prevSize = fw.Size
				continue
			}
			// File got bigger?
			if prevSize > 0 && prevSize < fw.Size {
				changes.NotifyModified()
				prevSize = fw.Size
				continue
			}
			prevSize = fw.Size

			// File was appended to (changed)?
			modTime := fi.ModTime()
			if modTime != prevModTime {
				prevModTime = modTime
				changes.NotifyModified()
			}
		}
	}()

	return changes, nil
}
