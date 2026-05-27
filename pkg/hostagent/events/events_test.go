// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// writeEvent appends a hostagent JSON event line to w.
func writeEvent(t *testing.T, w io.Writer, ev Event) {
	t.Helper()
	b, err := json.Marshal(ev)
	assert.NilError(t, err)
	_, err = fmt.Fprintln(w, string(b))
	assert.NilError(t, err)
}

// setupLogs creates empty ha.stdout.log / ha.stderr.log under a temp dir and
// returns file handles opened for appending. Callers write the content the
// test expects Watch to observe before calling Watch.
func setupLogs(t *testing.T) (stdoutPath, stderrPath string, stdoutW, stderrW *os.File) {
	t.Helper()
	dir := t.TempDir()
	stdoutPath = filepath.Join(dir, "ha.stdout.log")
	stderrPath = filepath.Join(dir, "ha.stderr.log")
	var err error
	stdoutW, err = os.Create(stdoutPath)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = stdoutW.Close() })
	stderrW, err = os.Create(stderrPath)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = stderrW.Close() })
	return stdoutPath, stderrPath, stdoutW, stderrW
}

func TestWatchStopOnCallbackTrue(t *testing.T) {
	stdoutPath, stderrPath, stdoutW, _ := setupLogs(t)
	// Three events in the file; the callback stops after 2.
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{SSHLocalPort: 22}})
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{Running: true}})
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{Exiting: true}})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var got []Event
	err := Watch(ctx, stdoutPath, stderrPath, time.Time{}, func(ev Event) bool {
		got = append(got, ev)
		return len(got) >= 2
	})
	assert.NilError(t, err)
	assert.Equal(t, len(got), 2, "expected Watch to stop after 2 events")
	assert.Equal(t, got[0].Status.SSHLocalPort, 22)
	assert.Assert(t, got[1].Status.Running)
}

func TestWatchBeginFilter(t *testing.T) {
	stdoutPath, stderrPath, stdoutW, _ := setupLogs(t)
	past := time.Now().Add(-time.Hour)
	begin := time.Now()
	future := time.Now().Add(time.Hour)

	writeEvent(t, stdoutW, Event{Time: past, Status: Status{SSHLocalPort: 22}})
	writeEvent(t, stdoutW, Event{Time: future, Status: Status{Running: true}})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var got []Event
	err := Watch(ctx, stdoutPath, stderrPath, begin, func(ev Event) bool {
		got = append(got, ev)
		return true
	})
	assert.NilError(t, err)
	assert.Equal(t, len(got), 1, "expected begin filter to skip the past event")
	assert.Assert(t, got[0].Status.Running)
}

func TestWatchIgnoresUnparseableLine(t *testing.T) {
	stdoutPath, stderrPath, stdoutW, _ := setupLogs(t)
	// One garbage line followed by a valid one; Watch must recover from the
	// parse error and deliver the next valid event instead of returning.
	_, err := fmt.Fprintln(stdoutW, "this is not json {")
	assert.NilError(t, err)
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{Running: true}})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var got []Event
	err = Watch(ctx, stdoutPath, stderrPath, time.Time{}, func(ev Event) bool {
		got = append(got, ev)
		return true
	})
	assert.NilError(t, err)
	assert.Equal(t, len(got), 1)
	assert.Assert(t, got[0].Status.Running)
}

func TestWatchReturnsOnCtxCancel(t *testing.T) {
	stdoutPath, stderrPath, stdoutW, _ := setupLogs(t)
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{SSHLocalPort: 22}})

	ctx, cancel := context.WithCancel(t.Context())

	received := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Watch(ctx, stdoutPath, stderrPath, time.Time{}, func(_ Event) bool {
			select {
			case received <- struct{}{}:
			default:
			}
			return false // never stop; rely on ctx cancel
		})
	}()

	// Wait for the pre-written event so we know Watch is live, then cancel.
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		cancel()
		<-done
		assert.Assert(t, false, "Watch did not deliver the first event")
	}
	cancel()

	select {
	case err := <-done:
		assert.NilError(t, err, "Watch returned error on ctx cancel")
	case <-time.After(5 * time.Second):
		assert.Assert(t, false, "Watch did not return within 5s of ctx cancel")
	}
}

// TestWatchPropagatesTailError forces the stdout tail to die with a
// fatal error and asserts that Watch surfaces that error to the
// caller, instead of returning nil when the Lines channel closes.
// The trigger differs by OS: POSIX uses a regular file as an
// intermediate path component (ENOTDIR); Windows uses a reserved
// character ('?') in a path component (ERROR_INVALID_NAME). Both are
// non-IsNotExist errors; tail.reopen wraps them and kills the tomb.
// On Windows, '?' is unambiguously rejected — unlike ':', which the
// NTFS alternate-data-stream syntax can coerce into a path-not-found
// that matches IsNotExist.
func TestWatchPropagatesTailError(t *testing.T) {
	dir := t.TempDir()

	var stdoutPath string
	if runtime.GOOS == "windows" {
		stdoutPath = filepath.Join(dir, "bad?dir", "ha.stdout.log")
	} else {
		intermediate := filepath.Join(dir, "not-a-dir")
		err := os.WriteFile(intermediate, []byte("x"), 0o644)
		assert.NilError(t, err)
		stdoutPath = filepath.Join(intermediate, "ha.stdout.log")
	}

	stderrPath := filepath.Join(dir, "ha.stderr.log")
	_, err := os.Create(stderrPath)
	assert.NilError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	err = Watch(ctx, stdoutPath, stderrPath, time.Time{}, func(_ Event) bool {
		return false
	})
	assert.ErrorContains(t, err, "ha.stdout.log")
}

func TestWatchDrainsStderrWithoutPropagating(t *testing.T) {
	stdoutPath, stderrPath, stdoutW, stderrW := setupLogs(t)
	_, err := fmt.Fprintln(stderrW, "hostagent: some stderr noise")
	assert.NilError(t, err)
	_, err = fmt.Fprintln(stderrW, "another stderr line")
	assert.NilError(t, err)
	writeEvent(t, stdoutW, Event{Time: time.Now(), Status: Status{Running: true}})

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	var got []Event
	err = Watch(ctx, stdoutPath, stderrPath, time.Time{}, func(ev Event) bool {
		got = append(got, ev)
		return true
	})
	assert.NilError(t, err)
	assert.Equal(t, len(got), 1, "stderr noise must not reach onEvent")
	assert.Assert(t, got[0].Status.Running)
}
