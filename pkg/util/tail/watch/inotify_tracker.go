// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

package watch

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
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
	// globally shared InotifyTracker; ensures only one fsnotify.Watcher is used.
	shared *InotifyTracker

	// these are used to ensure the shared InotifyTracker is run exactly once.
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

// track signals the run goroutine to begin watching the input filename.
func track(fname string) error {
	return watch(&watchInfo{
		fname: fname,
	})
}

// trackCreate signals the run goroutine to begin watching for the
// creation of a file with the input name. Callers must pair this with
// untrackCreate (not untrack) to deregister.
func trackCreate(fname string) error {
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

// untrack signals the run goroutine to remove the watch for the
// input filename previously registered with track.
func untrack(fname string) error {
	return remove(&watchInfo{
		fname: fname,
	})
}

// untrackCreate signals the run goroutine to remove the creation
// watch for the input filename previously registered with trackCreate.
func untrackCreate(fname string) error {
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

// eventsFor returns the channel of fsnotify events for the input filename.
// The channel is closed when untrack is called.
func eventsFor(fname string) <-chan fsnotify.Event {
	shared.mux.Lock()
	defer shared.mux.Unlock()

	return shared.chans[fname]
}

// Cleanup removes the watch for the input filename if necessary.
func Cleanup(fname string) error {
	return untrack(fname)
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

// removeWatch calls fsnotify.Remove for the input filename and closes the
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
		// remove() may close done and removeWatch() may close ch while we
		// are between the map lookup and the select. Both cases are then
		// select-ready; the send path panics on a closed ch. Dropping the
		// event is correct because the watch is being removed.
		defer func() { _ = recover() }()
		select {
		case ch <- event:
		case <-done:
		}
	}
}

// run starts three goroutines that multiplex a single fsnotify.Watcher
// across all tails in the process.
//
// Upstream nxadm/tail ran a single goroutine that did all three jobs
// from one select: service synchronous Add/Remove RPCs, drain
// watcher.Events, drain watcher.Errors. That deadlocks on Windows when
// the fsnotify reader thread needs to report an internal error while
// this goroutine is blocked inside a synchronous fsnotify.Add/Remove:
//
//   - fsnotify.readEvents calls sendError, which blocks on an unbuffered
//     Errors channel.
//   - Our RPC caller is blocked on <-in.reply from readEvents.
//   - Nobody is draining Errors, so readEvents never gets to process
//     the input; the RPC never returns; further Add/Remove calls pile
//     up on the unbuffered shared.watch channel.
//
// Splitting responsibilities avoids the cycle: the events drainer and
// the errors drainer run in their own goroutines that never issue
// fsnotify RPCs, so readEvents is always able to deliver whatever it
// produces. The RPC goroutine is still allowed to block on fsnotify —
// its blocking no longer starves the drainers.
func (t *InotifyTracker) run() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic("failed to create fsnotify.Watcher: " + err.Error())
	}
	t.watcher = watcher

	// Drain Events forever. Routes each event to the per-file channel
	// registered via addWatch.
	go func() {
		for event := range t.watcher.Events {
			t.sendEvent(event)
		}
	}()

	// Drain Errors forever. Historically this ran in the same select
	// as Add/Remove; splitting it out is the key to the deadlock fix
	// described above, since fsnotify's Errors channel is unbuffered
	// and readEvents blocks trying to send.
	go func() {
		for err := range t.watcher.Errors {
			if err == nil {
				continue
			}
			var sysErr *os.SyscallError
			if !errors.As(err, &sysErr) || !errors.Is(sysErr.Err, syscall.EINTR) {
				logger.Printf("Error in Watcher Error channel: %s", err)
			}
		}
	}()

	// Handle Add/Remove RPCs. Synchronous calls into fsnotify may
	// block until readEvents replies; that's fine here because the
	// drainers above keep readEvents unblocked.
	for {
		select {
		case winfo := <-t.watch:
			t.error <- t.addWatch(winfo)
		case winfo := <-t.remove:
			t.error <- t.removeWatch(winfo)
		}
	}
}
