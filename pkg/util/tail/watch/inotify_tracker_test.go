// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package watch

import (
	"sync"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// TestSendEventGuardsAgainstConcurrentClose verifies that sendEvent's
// recover() catches the send-on-closed-channel panic that arises when
// remove() closes done and removeWatch() closes ch between sendEvent's
// map lookup and its select. The guard drops the event, which is
// correct because the watch is being removed.
//
// The deterministic leg pre-closes both channels so every iteration's
// select has both cases ready; the Go runtime then picks each case
// with roughly equal probability, exercising the send path many times
// across the loop. The concurrent leg spawns a closer goroutine that
// mimics the remove/removeWatch ordering (done first, then ch) to
// cover the case where close(ch) happens after the map lookup has
// already returned a live reference.
//
// Without the guard, any iteration that picks the send path on a
// closed channel panics and the test binary crashes before the final
// log line.
func TestSendEventGuardsAgainstConcurrentClose(t *testing.T) {
	const fname = "test-file"

	// Deterministic: both channels pre-closed before the loop.
	t.Run("pre-closed", func(_ *testing.T) {
		tracker := &InotifyTracker{
			mux:       sync.Mutex{},
			chans:     make(map[string]chan fsnotify.Event),
			done:      make(map[string]chan bool),
			watchNums: make(map[string]int),
		}
		ch := make(chan fsnotify.Event)
		done := make(chan bool)
		tracker.chans[fname] = ch
		tracker.done[fname] = done
		close(done)
		close(ch)

		const iterations = 200
		for range iterations {
			tracker.sendEvent(fsnotify.Event{Name: fname})
		}
	})

	// Concurrent: race close(ch)/close(done) against sendEvent's select.
	t.Run("concurrent-close", func(_ *testing.T) {
		const iterations = 1000
		for range iterations {
			tracker := &InotifyTracker{
				mux:       sync.Mutex{},
				chans:     make(map[string]chan fsnotify.Event),
				done:      make(map[string]chan bool),
				watchNums: make(map[string]int),
			}
			ch := make(chan fsnotify.Event)
			done := make(chan bool)
			tracker.chans[fname] = ch
			tracker.done[fname] = done

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				tracker.sendEvent(fsnotify.Event{Name: fname})
			}()

			go func() {
				defer wg.Done()
				tracker.mux.Lock()
				delete(tracker.done, fname)
				close(done)
				tracker.mux.Unlock()

				tracker.mux.Lock()
				delete(tracker.chans, fname)
				close(ch)
				tracker.mux.Unlock()
			}()

			wg.Wait()
		}
	})
}
