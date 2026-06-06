// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	hostagentevents "github.com/lima-vm/lima/v2/pkg/hostagent/events"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
)

// instancePhase represents the hostagent lifecycle as observed by the watcher.
type instancePhase string

const (
	phaseUnknown  instancePhase = ""
	phaseStarting instancePhase = "Starting"
	phaseRunning  instancePhase = "Running"
	phaseStopped  instancePhase = "Stopped"
)

// instanceState tracks the watcher goroutine and its observed phase for one VM.
type instanceState struct {
	mu         sync.RWMutex
	phase      instancePhase
	cancel     context.CancelFunc // cancels the watcher goroutine
	done       chan struct{}      // closed when the watcher goroutine exits
	procExited chan struct{}      // closed when haCmd.Wait() returns (process dead)
	cmd        *exec.Cmd          // hostagent process, for graceful shutdown
}

// startWatcher launches a goroutine that tails the hostagent event stream and
// tracks lifecycle phase transitions. It also reaps the hostagent process via
// haCmd.Wait(), preventing zombies and detecting unclean exits.
//
// The watcher writes phase changes to in-memory state only; the reconciler
// reads this state and writes K8s conditions.
func (r *LimaVMReconciler) startWatcher(ctx context.Context, name, namespace string, haCmd *exec.Cmd, instDir string, begin time.Time) {
	watchCtx, watchCancel := context.WithCancel(ctx)

	state := &instanceState{
		phase:      phaseStarting,
		cancel:     watchCancel,
		done:       make(chan struct{}),
		procExited: make(chan struct{}),
		cmd:        haCmd,
	}

	r.instanceStatesMu.Lock()
	r.instanceStates[name] = state
	r.instanceStatesMu.Unlock()

	go r.runWatcher(watchCtx, name, namespace, instDir, begin, state)
}

// runWatcher is the watcher goroutine. It races haCmd.Wait() against
// events.Watch() so that process exit is detected even without an Exiting event
// (e.g. SIGKILL).
func (r *LimaVMReconciler) runWatcher(ctx context.Context, name, namespace, instDir string, begin time.Time, state *instanceState) {
	logger := log.FromContext(ctx).WithValues("instance", name, "component", "watcher")
	defer close(state.done)

	// Reap the hostagent process in a sub-goroutine. haCmd.Wait() blocks until
	// the process exits and cannot be cancelled via context. This is safe because
	// every code path that cancels the watcher also kills the hostagent process
	// (stopInstance, shutdownAllHostagents, stopInstanceForcibly on delete), so this
	// goroutine always returns promptly after cancellation.
	// When the process exits, cancel the Watch context so events.Watch returns.
	waitCtx, waitCancel := context.WithCancel(ctx)
	defer waitCancel()
	go func() {
		err := state.cmd.Wait()
		close(state.procExited)
		if err != nil {
			logger.Info("Hostagent process exited with error", "error", err)
		} else {
			logger.Info("Hostagent process exited cleanly")
		}
		waitCancel()
	}()

	haStdoutPath := filepath.Join(instDir, filenames.HostAgentStdoutLog)
	haStderrPath := filepath.Join(instDir, filenames.HostAgentStderrLog)

	onEvent := func(ev hostagentevents.Event) bool {
		state.mu.Lock()
		defer state.mu.Unlock()

		// Note: ev.Status.Degraded is intentionally not handled here.
		// Lima's hostagent does not currently emit Degraded events, so there
		// is nothing to act on. If Lima adds Degraded support in the future,
		// we should map it to a condition (e.g. Running=True with a degraded reason).

		if ev.Status.Exiting {
			logger.Info("Hostagent exiting")
			state.phase = phaseStopped
			r.enqueueReconcile(name, namespace)
			return true // stop watching
		}
		if ev.Status.Running {
			if state.phase != phaseRunning {
				logger.Info("Hostagent running")
				state.phase = phaseRunning
				r.enqueueReconcile(name, namespace)
			}
			return false
		}
		if ev.Status.SSHLocalPort > 0 && state.phase != phaseRunning {
			if state.phase != phaseStarting {
				logger.Info("Hostagent starting", "sshLocalPort", ev.Status.SSHLocalPort)
				state.phase = phaseStarting
				r.enqueueReconcile(name, namespace)
			}
		}
		return false
	}

	if err := hostagentevents.Watch(waitCtx, haStdoutPath, haStderrPath, begin, false, onEvent); err != nil {
		// Context cancellation is expected (process exit or controller shutdown).
		if waitCtx.Err() == nil {
			logger.Error(err, "Event watcher failed")
		}
	}

	// Ensure phase is Stopped after Watch returns, regardless of reason.
	state.mu.Lock()
	if state.phase != phaseStopped {
		state.phase = phaseStopped
		state.mu.Unlock()
		r.enqueueReconcile(name, namespace)
	} else {
		state.mu.Unlock()
	}

	logger.Info("Watcher stopped")
}

// stopWatcher cancels the watcher goroutine and waits for it to finish.
func (r *LimaVMReconciler) stopWatcher(name string) {
	r.instanceStatesMu.RLock()
	state, ok := r.instanceStates[name]
	r.instanceStatesMu.RUnlock()
	if !ok {
		return
	}

	state.cancel()
	<-state.done

	r.instanceStatesMu.Lock()
	delete(r.instanceStates, name)
	r.instanceStatesMu.Unlock()
}

// getInstancePhase returns the current observed phase for a VM instance.
// Returns phaseUnknown if no watcher exists.
func (r *LimaVMReconciler) getInstancePhase(name string) instancePhase {
	r.instanceStatesMu.RLock()
	state, ok := r.instanceStates[name]
	r.instanceStatesMu.RUnlock()
	if !ok {
		return phaseUnknown
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.phase
}

// waitForProcessExit waits for the hostagent process to exit. Returns true if
// the process exited, false if the context was cancelled first.
// Returns true immediately if no watcher exists for the instance.
func (r *LimaVMReconciler) waitForProcessExit(ctx context.Context, name string) bool {
	r.instanceStatesMu.RLock()
	state, ok := r.instanceStates[name]
	r.instanceStatesMu.RUnlock()
	if !ok {
		return true
	}
	select {
	case <-state.procExited:
		return true
	case <-ctx.Done():
		return false
	}
}

// signalHostagent sends a graceful shutdown signal to the hostagent process.
// Uses process.Interrupt which sends SIGINT on Unix and CTRL_BREAK on Windows
// (targeted at the hostagent's process group, not the parent).
// Returns false if no watcher exists, the process has already been reaped (its
// PID may be reused on Windows), or the signal could not be delivered.
func (r *LimaVMReconciler) signalHostagent(name string) bool {
	r.instanceStatesMu.RLock()
	state, ok := r.instanceStates[name]
	r.instanceStatesMu.RUnlock()
	if !ok || state.cmd == nil || state.cmd.Process == nil {
		return false
	}
	// Once procExited is closed, cmd.Wait() has released the OS process handle
	// and the PID may be reused; signalling it could deliver CTRL_BREAK to an
	// unrelated console process on Windows. While it is open the handle is
	// normally still held; cmd.Wait() releases it just before closing the
	// channel, leaving a brief window.
	select {
	case <-state.procExited:
		return false
	default:
	}
	return process.Interrupt(state.cmd.Process.Pid) == nil
}

// enqueueReconcile sends a GenericEvent to trigger a reconcile for the named VM.
func (r *LimaVMReconciler) enqueueReconcile(name, namespace string) {
	vm := &v1alpha1.LimaVM{}
	vm.Name = name
	vm.Namespace = namespace

	select {
	case r.reconcileChan <- event.TypedGenericEvent[*v1alpha1.LimaVM]{Object: vm}:
	default:
		// Channel full; a reconcile is already pending.
	}
}
