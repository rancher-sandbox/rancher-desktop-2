// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package hostswitch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"gotest.tools/v3/assert"
)

// TestRunLoop_CancellationEndsLoop asserts that a cancelled context ends the
// loop with a nil error, the behavior host-switch's clean shutdown relies on
// (and which no Windows-only test could prove).
func TestRunLoop_CancellationEndsLoop(t *testing.T) {
	const wantAttempts = 3
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	err := runLoop(ctx, logr.Discard(), time.Millisecond, func(context.Context) error {
		attempts++
		if attempts == wantAttempts {
			cancel()
		}
		return errors.New("bridge died")
	})
	assert.NilError(t, err)
	// The loop stops the moment ctx is cancelled, not before or after.
	assert.Equal(t, attempts, wantAttempts)
}

// TestRunLoop_RateLimitsRestarts asserts consecutive fast-failing attempts are
// spaced at least interval apart.
func TestRunLoop_RateLimitsRestarts(t *testing.T) {
	const (
		interval     = 30 * time.Millisecond
		wantAttempts = 3
	)
	ctx, cancel := context.WithCancel(context.Background())
	var starts []time.Time
	err := runLoop(ctx, logr.Discard(), interval, func(context.Context) error {
		starts = append(starts, time.Now())
		if len(starts) == wantAttempts {
			cancel()
		}
		return errors.New("bridge died")
	})
	assert.NilError(t, err)
	assert.Equal(t, len(starts), wantAttempts)
	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(starts[i-1])
		assert.Assert(t, gap >= interval, "attempt %d started %v after the previous, want >= %v", i, gap, interval)
	}
}

// TestRunLoop_NilExitIsRateLimited asserts the err == nil hot-loop guard: an
// attempt that returns nil without cancellation still backs off rather than
// spinning, and the loop remains cancellable.
func TestRunLoop_NilExitIsRateLimited(t *testing.T) {
	const (
		interval     = 30 * time.Millisecond
		wantAttempts = 2
	)
	ctx, cancel := context.WithCancel(context.Background())
	var starts []time.Time
	err := runLoop(ctx, logr.Discard(), interval, func(context.Context) error {
		starts = append(starts, time.Now())
		if len(starts) == wantAttempts {
			cancel()
		}
		return nil
	})
	assert.NilError(t, err)
	assert.Equal(t, len(starts), wantAttempts)
	gap := starts[1].Sub(starts[0])
	assert.Assert(t, gap >= interval, "nil-exit restart came %v after the previous, want >= %v (hot loop not guarded)", gap, interval)
}

// TestRunLoop_CancelDuringBackoff asserts the loop exits promptly when ctx is
// cancelled while it is waiting out the rate-limit backoff, rather than sleeping
// the full interval.
func TestRunLoop_CancelDuringBackoff(t *testing.T) {
	const interval = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	begin := time.Now()
	err := runLoop(ctx, logr.Discard(), interval, func(context.Context) error {
		attempts++
		return errors.New("bridge died")
	})
	elapsed := time.Since(begin)
	assert.NilError(t, err)
	// Cancel landed during backoff, so exactly one attempt ran.
	assert.Equal(t, attempts, 1)
	assert.Assert(t, elapsed < interval, "runLoop took %v, expected to exit during backoff well before %v", elapsed, interval)
}
