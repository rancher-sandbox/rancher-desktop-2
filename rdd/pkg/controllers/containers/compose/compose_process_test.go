// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	"k8s.io/apimachinery/pkg/types"
)

// fakeCommand is an in-memory test double for the command interface. It never
// spawns a real process: wait() blocks until either the test supplies a
// result via done, or kill() is called (which makes wait() return errKilled).
type fakeCommand struct {
	argv     []string
	done     chan error
	killed   chan struct{}
	killOnce sync.Once
	// outputStr is returned by output(); tests may set it before the command
	// completes to simulate captured stderr output.
	outputStr string
}

// errKilled is returned by fakeCommand.wait() when kill() won the race
// against a supplied result.
var errKilled = errors.New("signal: killed")

func newFakeCommand(argv []string) *fakeCommand {
	return &fakeCommand{
		argv:   argv,
		done:   make(chan error, 1),
		killed: make(chan struct{}),
	}
}

// args implements [command].
func (c *fakeCommand) args() []string { return c.argv }

// kill implements [command]. It causes a pending wait() to return errKilled.
func (c *fakeCommand) kill(context.Context) error {
	c.killOnce.Do(func() { close(c.killed) })
	return nil
}

// output implements [command].
func (c *fakeCommand) output() string { return c.outputStr }

// wait implements [command].
func (c *fakeCommand) wait(ctx context.Context) error {
	select {
	case err := <-c.done:
		return err
	case <-c.killed:
		return errKilled
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fakeProcessCommandExecutor returns a commandExecutor that produces fakeCommands
// instead of spawning real processes. resultFor is called with the command's
// args (e.g. []string{"compose", "up"}) to decide the outcome: if block is
// true, the returned command does not complete until kill() is called (or the
// test sends to its done channel directly); otherwise it completes
// immediately with result.
func fakeProcessCommandExecutor(resultFor func(args []string) (result error, block bool)) commandExecutor {
	return func(_ context.Context, _, name string, args ...string) (command, error) {
		c := newFakeCommand(append([]string{name}, args...))
		result, block := resultFor(args)
		if !block {
			c.done <- result
		}
		return c, nil
	}
}

// waitForProcessFinished polls t.procs[uid] until its processState is marked
// finished (i.e. the background `docker compose` command has exited), or
// fails the test after timeout.
func waitForProcessFinished(t *testing.T, procs *processTracker, uid types.UID, timeout time.Duration) processState {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		state, ok := procs.get(uid)
		assert.Assert(t, ok, "process state for %s not found", uid)
		if state.finished {
			return state
		}
		if time.Now().After(deadline) {
			assert.Assert(t, false, "timed out waiting for process to finish for %s", uid)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestProcessTracker_Run(t *testing.T) {
	t.Parallel()

	t.Run("aborts a previously running command for the same UID when the resource version changes", func(t *testing.T) {
		t.Parallel()

		tracker := &processTracker{
			executor: fakeProcessCommandExecutor(func(args []string) (error, bool) {
				// The "up" command blocks until killed; "down" completes immediately.
				return nil, len(args) > 0 && args[len(args)-1] == "up"
			}),
		}
		const uid = types.UID("project-uid")

		err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
		assert.NilError(t, err)

		firstState, ok := tracker.get(uid)
		assert.Assert(t, ok)
		firstCmd, ok := firstState.cmd.(*fakeCommand)
		assert.Assert(t, ok)

		// Starting a second command for the same project (new resource version)
		// should kill the first.
		err = tracker.run(t.Context(), uid, "rv2", "", "myproject", nil, []string{"down"}, func() {})
		assert.NilError(t, err)

		waitForProcessFinished(t, tracker, uid, 5*time.Second)

		secondState, ok := tracker.get(uid)
		assert.Assert(t, ok)
		assert.Assert(t, secondState.cmd != command(firstCmd), "expected a new process to replace the killed one")

		select {
		case <-firstCmd.killed:
		default:
			assert.Assert(t, false, "expected the first compose command to have been killed")
		}
	})

	t.Run("does not restart the command for a duplicate reconcile at the same resource version", func(t *testing.T) {
		t.Parallel()

		var executions int
		tracker := &processTracker{
			executor: fakeProcessCommandExecutor(func([]string) (error, bool) {
				executions++
				return nil, true // block, so the command is still "running" on the second call.
			}),
		}
		const uid = types.UID("project-uid")

		err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
		assert.NilError(t, err)

		err = tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
		assert.NilError(t, err)

		assert.Equal(t, executions, 1, "expected the command to only be started once")
	})

	t.Run("passes project identity (name and configs) as compose CLI arguments", func(t *testing.T) {
		t.Parallel()

		var gotArgs []string
		tracker := &processTracker{
			executor: fakeProcessCommandExecutor(func(args []string) (error, bool) {
				gotArgs = args
				return nil, false
			}),
		}
		const uid = types.UID("project-uid")

		err := tracker.run(
			t.Context(), uid, "rv1", "", "custom-project-name",
			[]string{"one.yaml", "two.yaml"}, []string{"up"}, func() {})
		assert.NilError(t, err)
		assert.DeepEqual(t, gotArgs, []string{
			"compose", "--project-name", "custom-project-name",
			"--file", "one.yaml", "--file", "two.yaml", "up",
		})

		err = tracker.run(
			t.Context(), uid, "rv2", "", "custom-project-name",
			[]string{"one.yaml", "two.yaml"}, []string{"down", "--remove-orphans", "--volumes"}, func() {})
		assert.NilError(t, err)
		assert.DeepEqual(t, gotArgs, []string{
			"compose", "--project-name", "custom-project-name",
			"--file", "one.yaml", "--file", "two.yaml", "down", "--remove-orphans", "--volumes",
		})
	})

	t.Run("calls onComplete once the process finishes", func(t *testing.T) {
		t.Parallel()

		tracker := &processTracker{
			executor: fakeProcessCommandExecutor(func([]string) (error, bool) { return nil, false }),
		}
		const uid = types.UID("project-uid")

		completed := make(chan struct{})
		err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() { close(completed) })
		assert.NilError(t, err)

		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			assert.Assert(t, false, "onComplete was not called")
		}

		state := waitForProcessFinished(t, tracker, uid, 5*time.Second)
		assert.NilError(t, state.err)
	})

	t.Run("does not restart the command for a duplicate concurrent reconcile while the first is starting", func(t *testing.T) {
		t.Parallel()

		var executions int
		startChan := make(chan struct{})
		unblockChan := make(chan struct{})

		tracker := &processTracker{
			executor: func(_ context.Context, _, _ string, args ...string) (command, error) {
				executions++
				close(startChan)
				<-unblockChan
				c := newFakeCommand(args)
				return c, nil
			},
		}
		const uid = types.UID("project-uid")

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
			assert.NilError(t, err)
		}()

		<-startChan

		err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
		assert.NilError(t, err)

		close(unblockChan)
		wg.Wait()

		assert.Equal(t, executions, 1, "expected the command to only be started once concurrently")
	})

	t.Run("aborts a concurrent start and replaces it when a newer resource version arrives", func(t *testing.T) {
		t.Parallel()

		var executions int
		startChan := make(chan struct{})
		unblockChan := make(chan struct{})

		tracker := &processTracker{
			executor: func(_ context.Context, _, _ string, args ...string) (command, error) {
				executions++
				if executions == 1 {
					close(startChan)
					<-unblockChan
				}
				c := newFakeCommand(args)
				return c, nil
			},
		}
		const uid = types.UID("project-uid")

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := tracker.run(t.Context(), uid, "rv1", "", "myproject", nil, []string{"up"}, func() {})
			assert.ErrorContains(t, err, "was restarted")
		}()

		<-startChan

		err := tracker.run(t.Context(), uid, "rv2", "", "myproject", nil, []string{"up"}, func() {})
		assert.NilError(t, err)

		close(unblockChan)
		wg.Wait()

		assert.Equal(t, executions, 2, "expected both commands to be spawned")

		state, ok := tracker.get(uid)
		assert.Assert(t, ok)
		assert.Equal(t, state.resourceVersion, "rv2")
	})
}

func TestProcessTracker_Abort(t *testing.T) {
	t.Parallel()

	t.Run("kills a running process and removes it from the tracker", func(t *testing.T) {
		t.Parallel()

		tracker := &processTracker{procs: make(map[types.UID]processState)}
		cmd := newFakeCommand([]string{"compose", "up"})
		const uid = types.UID("project-uid")
		tracker.procs[uid] = processState{cmd: cmd}

		assert.NilError(t, tracker.abort(t.Context(), uid))

		select {
		case <-cmd.killed:
		default:
			assert.Assert(t, false, "expected the tracked process to have been killed")
		}

		_, ok := tracker.get(uid)
		assert.Assert(t, !ok, "process state should be removed once aborted")
	})

	t.Run("does not attempt to kill an already-finished process", func(t *testing.T) {
		t.Parallel()

		tracker := &processTracker{procs: make(map[types.UID]processState)}
		cmd := newFakeCommand([]string{"compose", "up"})
		const uid = types.UID("project-uid")
		tracker.procs[uid] = processState{cmd: cmd, finished: true}

		assert.NilError(t, tracker.abort(t.Context(), uid))

		select {
		case <-cmd.killed:
			assert.Assert(t, false, "an already-finished process should not be killed")
		default:
		}

		_, ok := tracker.get(uid)
		assert.Assert(t, !ok, "process state should be removed once aborted")
	})

	t.Run("is a no-op when there is no tracked process", func(t *testing.T) {
		t.Parallel()

		tracker := &processTracker{procs: make(map[types.UID]processState)}
		assert.NilError(t, tracker.abort(t.Context(), types.UID("no-such-uid")))
	})
}
