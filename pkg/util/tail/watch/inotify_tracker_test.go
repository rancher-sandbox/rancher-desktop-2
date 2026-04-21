// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package watch

import (
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// TestSendEventNoPanicOnConcurrentClose verifies that sendEvent does not
// panic when ch and done are both closed concurrently by remove() plus
// removeWatch(). Splitting InotifyTracker.run() into three goroutines
// made the close races observable: the select in sendEvent can enter
// with both cases ready, and the send-on-closed-channel case panics
// when picked. The deferred recover in sendEvent drops the event,
// which is the correct behaviour when the watch is being removed.
//
// Without the recover, each iteration panics on the send case with
// ~50% probability. 100 iterations make a missed regression vanishingly
// unlikely (1 - 0.5^100).
func TestSendEventNoPanicOnConcurrentClose(_ *testing.T) {
	tracker := &InotifyTracker{
		mux:       sync.Mutex{},
		chans:     make(map[string]chan fsnotify.Event),
		done:      make(map[string]chan bool),
		watchNums: make(map[string]int),
	}

	const fname = "test-file"
	ch := make(chan fsnotify.Event)
	done := make(chan bool)
	tracker.chans[fname] = ch
	tracker.done[fname] = done
	close(done)
	close(ch)

	for range 100 {
		tracker.sendEvent(fsnotify.Event{Name: fname})
	}
}
