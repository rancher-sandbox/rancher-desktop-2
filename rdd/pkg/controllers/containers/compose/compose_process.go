// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// processState tracks the state of a running `docker compose` command
// started on behalf of some UID (a Compose or ComposeUpRequest object).
type processState struct {
	// The resource version of the object when the process was started.
	resourceVersion string
	// The process being run.
	cmd command
	// Whether the process is in the process of being started.
	starting bool
	// Whether the process has terminated, successfully or not.
	finished bool
	// The error returned by the process, only valid if finished is true.
	err error
}

// processTracker tracks in-flight `docker compose` commands, keyed by the UID
// of the object (Compose or ComposeUpRequest) that requested the command.
type processTracker struct {
	sync.Mutex
	executor commandExecutor
	procs    map[types.UID]processState
}

// newProcessTracker creates a new processTracker with the default command
// executor.
func newProcessTracker() *processTracker {
	return &processTracker{
		executor: defaultCommandExecutor,
		procs:    make(map[types.UID]processState),
	}
}

// get returns the tracked process state for uid, if any.
func (t *processTracker) get(uid types.UID) (processState, bool) {
	t.Lock()
	defer t.Unlock()
	state, ok := t.procs[uid]
	return state, ok
}

// delete removes and returns the tracked process state for uid, if any.
func (t *processTracker) delete(uid types.UID) (processState, bool) {
	t.Lock()
	defer t.Unlock()
	state, ok := t.procs[uid]
	delete(t.procs, uid)
	return state, ok
}

// abort kills (if necessary) and removes the tracked process for uid.
func (t *processTracker) abort(ctx context.Context, uid types.UID) error {
	state, ok := t.delete(uid)
	if !ok || state.finished || state.cmd == nil {
		return nil
	}
	return state.cmd.kill(ctx)
}

// run starts a `docker compose` command for the given projectName (passed as
// --project-name) and configs (passed as --file, relative to workingDir),
// tracked under uid at the given resourceVersion. If a command is already
// tracked for uid at the same resourceVersion, this is a no-op (a duplicate
// reconcile). If a different (stale) command is tracked, it is aborted first.
//
// The given context must last long enough for the command to finish; this
// typically means the context passed to the reconcile function is not suitable.
// onComplete is called once the command exits, only if the tracked state was
// not superseded by a newer call to run in the meantime.
func (t *processTracker) run(
	ctx context.Context,
	uid types.UID,
	resourceVersion string,
	workingDir string,
	projectName string,
	configs []string,
	args []string,
	onComplete func(),
) error {
	// TODO: Support nerdctl here.
	cli := "docker"

	// Compose derives the project name and looks up config files relative to
	// the current directory by default; pass the project's own identity
	// explicitly so this always targets the right project, regardless of
	// workingDir's directory name or which config file names are in use.
	composeArgs := []string{"compose", "--project-name", projectName}
	for _, config := range configs {
		composeArgs = append(composeArgs, "--file", config)
	}
	composeArgs = append(composeArgs, args...)

	// Check if there's a concurrent start.
	t.Lock()
	if t.procs == nil {
		t.procs = make(map[types.UID]processState)
	}
	state := t.procs[uid]
	t.Unlock()
	if state.cmd != nil || state.starting {
		if state.resourceVersion == resourceVersion {
			// The resource version hasn't changed since the process was started;
			// this is a duplicate reconcile request. Don't restart the process.
			return nil
		}
		// Not the same resource version; abort if the command is still running.
		if state.cmd != nil {
			if err := state.cmd.kill(ctx); err != nil {
				return fmt.Errorf("failed to kill existing compose command for %s: %w", projectName, err)
			}
		}
	}

	// No relevant concurrent starts; mark that we're starting.
	t.Lock()
	if t.procs[uid].resourceVersion != state.resourceVersion {
		// There was a race: somebody else updated the state.
		t.Unlock()
		return fmt.Errorf("compose command for %s was restarted", projectName)
	}
	state = processState{resourceVersion: resourceVersion, starting: true}
	t.procs[uid] = state
	t.Unlock()

	// Actually start the process.
	var err error
	state.cmd, err = t.executor(ctx, workingDir, cli, composeArgs...)
	if err != nil {
		// Remove the stale state
		t.Lock()
		if s := t.procs[uid]; s.resourceVersion == resourceVersion {
			delete(t.procs, uid)
		}
		t.Unlock()
		return fmt.Errorf("failed to run compose command for %s: %w", projectName, err)
	}
	state.starting = false

	// Update the state with the new process, and mark the process as started.
	t.Lock()
	if t.procs[uid].resourceVersion != resourceVersion {
		// There was a race: somebody else updated the state.
		t.Unlock()
		_ = state.cmd.kill(ctx)
		return fmt.Errorf("compose command for %s was restarted", projectName)
	}
	t.procs[uid] = state
	t.Unlock()

	go func() {
		state.err = state.cmd.wait(ctx)
		state.finished = true
		updated := false
		t.Lock()
		if current, ok := t.procs[uid]; ok && current.cmd == state.cmd {
			t.procs[uid] = state
			updated = true
		}
		t.Unlock()
		if updated {
			// Per doc comment, only call if we are still the active process.
			onComplete()
		}
	}()

	return nil
}
