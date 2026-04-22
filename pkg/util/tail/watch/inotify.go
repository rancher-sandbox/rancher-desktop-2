// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

package watch

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/tomb.v1"
)

// InotifyFileWatcher uses inotify (via fsnotify) to monitor file changes.
type InotifyFileWatcher struct {
	Filename string
	Size     int64
}

// NewInotifyFileWatcher returns a FileWatcher that uses fsnotify for
// the given filename. The process-global InotifyTracker is used to
// multiplex events across all watchers sharing a single fsnotify.Watcher.
func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher{filepath.Clean(filename), 0}
	return fw
}

// BlockUntilExists blocks until the file appears or the tomb is dying.
// On Linux it subscribes to IN_CREATE on the parent directory; on
// Windows it subscribes to directory rename/create events.
func (fw *InotifyFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	err := trackCreate(fw.Filename)
	if err != nil {
		return err
	}
	defer func() { _ = untrackCreate(fw.Filename) }()

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	events := eventsFor(fw.Filename)

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return errors.New("inotify watcher has been closed")
			}
			evtName, err := filepath.Abs(evt.Name)
			if err != nil {
				return err
			}
			fwFilename, err := filepath.Abs(fw.Filename)
			if err != nil {
				return err
			}
			if evtName == fwFilename {
				return nil
			}
		case <-t.Dying():
			return tomb.ErrDying
		}
	}
}

// ChangeEvents subscribes to file-level notifications via fsnotify.
// It spawns a goroutine that translates raw fsnotify events into the
// FileChanges notification channels and terminates when the tomb dies
// or the file is deleted or renamed. wg is incremented before the
// goroutine is spawned and Done when the goroutine finishes its untrack
// so callers can synchronise teardown with the shared InotifyTracker.
func (fw *InotifyFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64, wg *sync.WaitGroup) (*FileChanges, error) {
	err := track(fw.Filename)
	if err != nil {
		return nil, err
	}

	changes := NewFileChanges()
	fw.Size = pos

	wg.Add(1)
	go func() {
		defer wg.Done()
		events := eventsFor(fw.Filename)

		for {
			prevSize := fw.Size

			var evt fsnotify.Event
			var ok bool

			select {
			case evt, ok = <-events:
				if !ok {
					_ = untrack(fw.Filename)
					return
				}
			case <-t.Dying():
				_ = untrack(fw.Filename)
				return
			}

			switch {
			case evt.Op&fsnotify.Remove == fsnotify.Remove,
				evt.Op&fsnotify.Rename == fsnotify.Rename:
				_ = untrack(fw.Filename)
				changes.NotifyDeleted()
				return

			// With an open fd, unlink(fd) - inotify returns IN_ATTRIB (==fsnotify.Chmod)
			case evt.Op&fsnotify.Chmod == fsnotify.Chmod,
				evt.Op&fsnotify.Write == fsnotify.Write:
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					// Treat any stat failure (IsNotExist, permission,
					// transient I/O) as a deletion: the tail reopens on the
					// next cycle or exits cleanly. Panicking would crash
					// the host process, because this runs in a detached
					// goroutine the caller cannot recover from.
					_ = untrack(fw.Filename)
					changes.NotifyDeleted()
					return
				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					changes.NotifyTruncated()
				} else {
					changes.NotifyModified()
				}
			}
		}
	}()

	return changes, nil
}
