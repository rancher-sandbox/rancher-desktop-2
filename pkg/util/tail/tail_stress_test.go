// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package tail_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

// writerEnvVar makes the test binary run as a subprocess that writes
// JSON event lines to its stdout until stdin is closed. We self-exec
// (see TestMain) because an external writer process produces the
// ReadDirectoryChanges traffic volume needed to trigger fsnotify's
// "buffer larger than it is" error path on Windows; an in-process
// goroutine writer does not.
const writerEnvVar = "TAIL_TEST_WRITER"

// stressEnvVar opts in to TestInotifyTrackerNoDeadlockOnRepeatedRotation.
// The test takes ~7 minutes, so it runs only when explicitly requested.
const stressEnvVar = "TAIL_STRESS"

func TestMain(m *testing.M) {
	if os.Getenv(writerEnvVar) == "1" {
		runFakeHostagentSubprocess()
		return
	}
	os.Exit(m.Run())
}

// TestInotifyTrackerNoDeadlockOnRepeatedRotation exercises the shared
// InotifyTracker with a log-rotation pattern: rotate an existing log
// file to a numbered backup, create a fresh one, run a Tail against it,
// then stop. This mirrors what a long-running process that tails a
// rotating log does on every rotation cycle.
//
// On the pre-fix tracker (single-goroutine run() that serviced Add/Remove
// RPCs and drained watcher.Events/Errors from one select) this
// reliably hangs on Windows under CPU pressure because:
//
//  1. fsnotify's readEvents goroutine can call sendError while holding the
//     I/O thread lock — and fsnotify's Errors channel is unbuffered.
//  2. While the tracker's run goroutine is inside a synchronous
//     fsnotify.Remove / fsnotify.Add call (blocked on <-in.reply from
//     readEvents), it is not draining watcher.Errors.
//  3. readEvents blocks trying to deliver the error; it therefore does
//     not process the pending input; the Remove/Add never returns;
//     every later tail piles up on the unbuffered shared.watch channel.
//
// The test forces GOMAXPROCS=1 so a single CPU serializes the scheduler,
// closely mirroring the CI windows-latest 2-vCPU runner under Compat
// Telemetry noise. It spawns an external writer process each cycle
// because only external-process writes produce enough ReadDirectoryChanges
// volume to trigger fsnotify's internal overflow error. With the
// three-goroutine fix every cycle finishes cleanly.
func TestInotifyTrackerNoDeadlockOnRepeatedRotation(t *testing.T) {
	if testing.Short() || os.Getenv(stressEnvVar) != "1" {
		t.Skipf("set %s=1 to run the stress test", stressEnvVar)
	}
	prev := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(prev) })

	const (
		cycles          = 200
		writerDuration  = 2 * time.Second
		cycleStopBudget = 10 * time.Second
	)

	selfExe, err := os.Executable()
	assert.NilError(t, err, "locate test binary")

	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "ha.stdout.log")
	stderrPath := filepath.Join(dir, "ha.stderr.log")

	for i := 1; i <= cycles; i++ {
		runRotationCycle(t, i, selfExe, stdoutPath, stderrPath, writerDuration, cycleStopBudget)
	}
}

func runRotationCycle(t *testing.T, cycle int, selfExe, stdoutPath, stderrPath string, writerDuration, stopBudget time.Duration) {
	t.Helper()

	// Rotate existing logs to {path}.{cycle}.log and create fresh files,
	// matching pkg/util/logfile.Create.
	stderrW := rotateAndCreate(t, stderrPath, cycle)
	stdoutW := rotateAndCreate(t, stdoutPath, cycle)

	// Spawn the writer subprocess. cmd.Start returns as soon as the
	// process is alive; the child exits when we close stdin, and the
	// per-cycle cleanup below reaps it regardless of ctx state. Using
	// t.Context() is a backstop: if the test panics before Wait runs,
	// testing's cleanup still kills the subprocess.
	cmd := exec.CommandContext(t.Context(), selfExe, "-test.run=^$") // no-op test selector; TestMain branches on env
	cmd.Env = append(os.Environ(), writerEnvVar+"=1")
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	stdin, err := cmd.StdinPipe()
	assert.NilError(t, err, "cycle %d: stdin pipe", cycle)
	assert.NilError(t, cmd.Start(), "cycle %d: start writer", cycle)
	stdoutW.Close()
	stderrW.Close()

	// Tails both files. Counts received lines.
	var received atomic.Int64
	tailCtx, tailCancel := context.WithCancel(t.Context())
	tailDone := make(chan error, 1)
	go func() {
		tailDone <- runDualTail(tailCtx, stdoutPath, stderrPath, &received)
	}()

	// Let the writer run, then signal it to stop and reap it.
	time.Sleep(writerDuration)
	_ = stdin.Close()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-waitDone
	}

	time.Sleep(100 * time.Millisecond)
	tailCancel()

	select {
	case err := <-tailDone:
		assert.NilError(t, err, "cycle %d: tail returned error", cycle)
		assert.Assert(t, received.Load() > 0,
			"cycle %d: received 0 events (writer wrote to %s but tail saw nothing)",
			cycle, stdoutPath)
	case <-time.After(stopBudget):
		assert.Assert(t, false,
			"cycle %d: tail did not return within %s; received=%d\n\n%s",
			cycle, stopBudget, received.Load(), goroutineDump())
	}
}

func rotateAndCreate(t *testing.T, path string, cycle int) *os.File {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		numbered := fmt.Sprintf("%s.%d.log", path, cycle)
		assert.NilError(t, os.Rename(path, numbered), "rotate %s", path)
	}
	f, err := os.Create(path)
	assert.NilError(t, err, "create %s", path)
	return f
}

type fakeStatus struct {
	SSHLocalPort int  `json:"sshLocalPort,omitempty"`
	Running      bool `json:"running,omitempty"`
	Exiting      bool `json:"exiting,omitempty"`
}

type fakeEvent struct {
	Time   time.Time  `json:"time"`
	Status fakeStatus `json:"status"`
}

// runFakeHostagentSubprocess is invoked by TestMain when the child
// process is launched. It writes JSON event lines to os.Stdout at a
// steady rate and exits when os.Stdin is closed by the parent.
func runFakeHostagentSubprocess() {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(fakeEvent{Time: time.Now(), Status: fakeStatus{SSHLocalPort: 22}})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
		}
	}()

	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	start := time.Now()
	runningSent := false

	for {
		select {
		case <-done:
			_ = enc.Encode(fakeEvent{Time: time.Now(), Status: fakeStatus{Exiting: true}})
			return
		case <-deadline.C:
			return
		case <-tick.C:
			if !runningSent && time.Since(start) >= 200*time.Millisecond {
				_ = enc.Encode(fakeEvent{Time: time.Now(), Status: fakeStatus{SSHLocalPort: 22, Running: true}})
				runningSent = true
			}
			// Burst a handful of events per tick so fsnotify's 4KB buffer
			// has a chance to overflow and trigger the internal sendError
			// path that deadlocks the shared tracker.
			for range 10 {
				_ = enc.Encode(fakeEvent{Time: time.Now(), Status: fakeStatus{SSHLocalPort: 22}})
			}
		}
	}
}

func runDualTail(ctx context.Context, stdoutPath, stderrPath string, received *atomic.Int64) error {
	cfg := tail.Config{Follow: true, ReOpen: true, MustExist: false, Logger: tail.DiscardingLogger}
	stdoutT, err := tail.Open(stdoutPath, cfg)
	if err != nil {
		return fmt.Errorf("open stdout: %w", err)
	}
	stderrT, err := tail.Open(stderrPath, cfg)
	if err != nil {
		_ = stdoutT.Stop()
		return fmt.Errorf("open stderr: %w", err)
	}

loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case line, ok := <-stdoutT.Lines:
			if !ok || line == nil {
				break loop
			}
			received.Add(1)
		case line, ok := <-stderrT.Lines:
			if !ok || line == nil {
				break loop
			}
		}
	}

	// Match Lima's events.Watch: intentionally no Cleanup() between re-tails.
	done := make(chan struct{})
	go func() {
		_ = stdoutT.Stop()
		_ = stderrT.Stop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("tail.Stop did not return within 5s")
	}
}

func goroutineDump() string {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}
