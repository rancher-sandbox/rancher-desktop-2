// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

package watch

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/tomb.v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/nxadmtail/util"
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
	err := WatchCreate(fw.Filename)
	if err != nil {
		return err
	}
	defer func() { _ = RemoveWatchCreate(fw.Filename) }()

	// Do a real check now as the file might have been created before
	// calling `WatchFlags` above.
	if _, err = os.Stat(fw.Filename); !os.IsNotExist(err) {
		// file exists, or stat returned an error.
		return err
	}

	events := Events(fw.Filename)

	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return fmt.Errorf("inotify watcher has been closed")
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
// or the file is deleted or renamed.
func (fw *InotifyFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64) (*FileChanges, error) {
	err := Watch(fw.Filename)
	if err != nil {
		return nil, err
	}

	changes := NewFileChanges()
	fw.Size = pos

	go func() {
		events := Events(fw.Filename)

		for {
			prevSize := fw.Size

			var evt fsnotify.Event
			var ok bool

			select {
			case evt, ok = <-events:
				if !ok {
					_ = RemoveWatch(fw.Filename)
					return
				}
			case <-t.Dying():
				_ = RemoveWatch(fw.Filename)
				return
			}

			switch {
			case evt.Op&fsnotify.Remove == fsnotify.Remove:
				fallthrough

			case evt.Op&fsnotify.Rename == fsnotify.Rename:
				_ = RemoveWatch(fw.Filename)
				changes.NotifyDeleted()
				return

			// With an open fd, unlink(fd) - inotify returns IN_ATTRIB (==fsnotify.Chmod)
			case evt.Op&fsnotify.Chmod == fsnotify.Chmod:
				fallthrough

			case evt.Op&fsnotify.Write == fsnotify.Write:
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					if os.IsNotExist(err) {
						_ = RemoveWatch(fw.Filename)
						changes.NotifyDeleted()
						return
					}
					// XXX: report this error back to the user
					util.Fatal("Failed to stat file %v: %v", fw.Filename, err)
				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					changes.NotifyTruncated()
				} else {
					changes.NotifyModified()
				}
				prevSize = fw.Size
			}
		}
	}()

	return changes, nil
}
