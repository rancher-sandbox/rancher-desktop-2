// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

package watch

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/nxadmtail/util"
)

// InotifyTracker multiplexes a single fsnotify.Watcher across many
// per-file consumers in the same process. A process-wide singleton
// (returned by the unexported goRun) is used so inotify instance
// budgets (fs.inotify.max_user_instances == 128 by default on Linux)
// are not exhausted when many tails run concurrently.
type InotifyTracker struct {
	mux       sync.Mutex
	watcher   *fsnotify.Watcher
	chans     map[string]chan fsnotify.Event
	done      map[string]chan bool
	watchNums map[string]int
	watch     chan *watchInfo
	remove    chan *watchInfo
	error     chan error
}

type watchInfo struct {
	op    fsnotify.Op
	fname string
}

func (w *watchInfo) isCreate() bool {
	return w.op == fsnotify.Create
}

var (
	// globally shared InotifyTracker; ensures only one fsnotify.Watcher is used
	shared *InotifyTracker

	// these are used to ensure the shared InotifyTracker is run exactly once
	once  = sync.Once{}
	goRun = func() {
		shared = &InotifyTracker{
			mux:       sync.Mutex{},
			chans:     make(map[string]chan fsnotify.Event),
			done:      make(map[string]chan bool),
			watchNums: make(map[string]int),
			watch:     make(chan *watchInfo),
			remove:    make(chan *watchInfo),
			error:     make(chan error),
		}
		go shared.run()
	}

	logger = log.New(os.Stderr, "", log.LstdFlags)
)

// Watch signals the run goroutine to begin watching the input filename.
func Watch(fname string) error {
	return watch(&watchInfo{
		fname: fname,
	})
}

// WatchCreate signals the run goroutine to begin watching for the
// creation of a file with the input name. Callers must pair this with
// RemoveWatchCreate (not RemoveWatch) to deregister.
func WatchCreate(fname string) error {
	return watch(&watchInfo{
		op:    fsnotify.Create,
		fname: fname,
	})
}

func watch(winfo *watchInfo) error {
	// start running the shared InotifyTracker if not already running
	once.Do(goRun)

	winfo.fname = filepath.Clean(winfo.fname)
	shared.watch <- winfo
	return <-shared.error
}

// RemoveWatch signals the run goroutine to remove the watch for the
// input filename previously registered with Watch.
func RemoveWatch(fname string) error {
	return remove(&watchInfo{
		fname: fname,
	})
}

// RemoveWatchCreate signals the run goroutine to remove the creation
// watch for the input filename previously registered with WatchCreate.
func RemoveWatchCreate(fname string) error {
	return remove(&watchInfo{
		op:    fsnotify.Create,
		fname: fname,
	})
}

func remove(winfo *watchInfo) error {
	// start running the shared InotifyTracker if not already running
	once.Do(goRun)

	winfo.fname = filepath.Clean(winfo.fname)
	shared.mux.Lock()
	done := shared.done[winfo.fname]
	if done != nil {
		delete(shared.done, winfo.fname)
		close(done)
	}
	shared.mux.Unlock()

	shared.remove <- winfo
	return <-shared.error
}

// Events returns the channel of fsnotify events for the input filename.
// The channel is closed when RemoveWatch is called.
func Events(fname string) <-chan fsnotify.Event {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	return shared.chans[fname]
}

// Cleanup removes the watch for the input filename if necessary.
func Cleanup(fname string) error {
	return RemoveWatch(fname)
}

// watchFlags calls fsnotify.WatchFlags for the input filename and flags, creating
// a new Watcher if the previous Watcher was closed.
func (t *InotifyTracker) addWatch(winfo *watchInfo) error {
	t.mux.Lock()
	defer t.mux.Unlock()

	if t.chans[winfo.fname] == nil {
		t.chans[winfo.fname] = make(chan fsnotify.Event)
	}
	if t.done[winfo.fname] == nil {
		t.done[winfo.fname] = make(chan bool)
	}

	fname := winfo.fname
	if winfo.isCreate() {
		// Watch for new files to be created in the parent directory.
		fname = filepath.Dir(fname)
	}

	var err error
	// already in inotify watch
	if t.watchNums[fname] == 0 {
		err = t.watcher.Add(fname)
	}
	if err == nil {
		t.watchNums[fname]++
	}
	return err
}

// removeWatch calls fsnotify.RemoveWatch for the input filename and closes the
// corresponding events channel.
func (t *InotifyTracker) removeWatch(winfo *watchInfo) error {
	t.mux.Lock()

	ch := t.chans[winfo.fname]
	if ch != nil {
		delete(t.chans, winfo.fname)
		close(ch)
	}

	fname := winfo.fname
	if winfo.isCreate() {
		// Watch for new files to be created in the parent directory.
		fname = filepath.Dir(fname)
	}
	t.watchNums[fname]--
	watchNum := t.watchNums[fname]
	if watchNum == 0 {
		delete(t.watchNums, fname)
	}
	t.mux.Unlock()

	var err error
	// If we were the last ones to watch this file, unsubscribe from inotify.
	// This needs to happen after releasing the lock because fsnotify waits
	// synchronously for the kernel to acknowledge the removal of the watch
	// for this file, which causes us to deadlock if we still held the lock.
	if watchNum == 0 {
		err = t.watcher.Remove(fname)
	}

	return err
}

// sendEvent sends the input event to the appropriate Tail.
func (t *InotifyTracker) sendEvent(event fsnotify.Event) {
	name := filepath.Clean(event.Name)

	t.mux.Lock()
	ch := t.chans[name]
	done := t.done[name]
	t.mux.Unlock()

	if ch != nil && done != nil {
		select {
		case ch <- event:
		case <-done:
		}
	}
}

// run starts the goroutine in which the shared struct reads events from its
// Watcher's Event channel and sends the events to the appropriate Tail.
func (t *InotifyTracker) run() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		util.Fatal("failed to create Watcher")
	}
	t.watcher = watcher

	for {
		select {
		case winfo := <-t.watch:
			t.error <- t.addWatch(winfo)

		case winfo := <-t.remove:
			t.error <- t.removeWatch(winfo)

		case event, open := <-t.watcher.Events:
			if !open {
				return
			}
			t.sendEvent(event)

		case err, open := <-t.watcher.Errors:
			if !open {
				return
			} else if err != nil {
				sysErr, ok := err.(*os.SyscallError)
				if !ok || sysErr.Err != syscall.EINTR {
					logger.Printf("Error in Watcher Error channel: %s", err)
				}
			}
		}
	}
}
