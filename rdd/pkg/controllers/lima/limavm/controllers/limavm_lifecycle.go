// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-logr/logr"
	limainstance "github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/store"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/logfile"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/wsl"
)

// wsl2RootDisk is the root-disk filename Lima's WSL2 driver creates when it
// imports a distro with `wsl.exe --import`.
const wsl2RootDisk = "ext4.vhdx"

func (r *LimaVMReconciler) handleDeletion(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	r.stopWatcher(limaVM.Name)

	// Stop and delete the Lima instance.
	// Inspect may fail if the instance doesn't exist, which is fine during
	// deletion - we just proceed to remove the finalizer.
	existingInst, err := store.Inspect(ctx, limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to inspect Lima instance for deletion")
	}
	if existingInst != nil {
		forceStopForDeletion(ctx, logger, existingInst)
		preserveInstanceLogs(ctx, existingInst)
		logger.Info("Deleting Lima instance", "instance", limaVM.Name)
		// Use a timeout because Lima's WSL2 driver calls wsl.exe --unregister
		// which can hang indefinitely if the WSL subsystem is degraded.
		deleteCtx, deleteCancel := context.WithTimeout(ctx, time.Minute)
		err = limainstance.Delete(deleteCtx, existingInst, true)
		deleteCancel()
		if err != nil {
			logger.Error(err, "Failed to delete Lima instance")
			return ctrl.Result{}, err
		}
		logger.Info("Deleted Lima instance", "instance", limaVM.Name)
	}

	// Delete owned resources and remove the finalizer in one pass. This is safe
	// because owned resources (ConfigMaps) delete instantly and have no finalizers.
	// If we later own resources with complex teardown, split this into two reconcile
	// cycles: delete owned resources, then verify they are gone before removing
	// the finalizer.
	if err := base.DeleteOwnedResources(ctx, r.Client, limaVM, r.Manager); err != nil {
		logger.Error(err, "Failed to delete owned resources")
		return ctrl.Result{}, err
	}

	// Remove finalizer
	if err := base.RemoveCleanupFinalizer(ctx, r.Client, limaVM); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Deleted Lima instance, owned resources, and finalizer")
	return ctrl.Result{}, nil
}

// handleRestartAnnotation translates the restartRequested annotation into
// status.restartNeeded. It takes two reconcile cycles:
//  1. Set status.restartNeeded=true (if not already set).
//  2. Remove the annotation (status is already persisted).
//
// This ordering ensures the status is durable before metadata changes.
// If the annotation removal fails, the next reconcile sees both and retries.
func (r *LimaVMReconciler) handleRestartAnnotation(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !limaVM.Status.RestartNeeded {
		logger.Info("Restart requested via annotation, setting status.restartNeeded")
		patch := client.MergeFrom(limaVM.DeepCopy())
		limaVM.Status.RestartNeeded = true
		if err := r.Status().Patch(ctx, limaVM, patch); err != nil {
			logger.Error(err, "Failed to set status.restartNeeded")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// status.restartNeeded is already true; remove the annotation.
	logger.Info("Removing restartRequested annotation")
	patch := client.MergeFrom(limaVM.DeepCopy())
	delete(limaVM.Annotations, v1alpha1.AnnotationRestartRequested)
	if err := r.Patch(ctx, limaVM, patch); err != nil {
		logger.Error(err, "Failed to remove restartRequested annotation")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// handleRunningState manages VM start/stop based on spec.running and the
// watcher's observed phase. When a watcher is active, its phase is the source
// of truth. When no watcher exists (controller restart), store.Inspect detects
// orphaned hostagents.
func (r *LimaVMReconciler) handleRunningState(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	shouldRun := limaVM.Spec.Running
	phase := r.getInstancePhase(limaVM.Name)

	logger.Info("Checking running state", "shouldRun", shouldRun, "phase", phase)

	// After a controller restart, the Running condition may be stale (persisted
	// in kine from the previous controller lifetime). Without a watcher, we
	// cannot verify it, so reset it to Unknown before proceeding.
	// Guard with shouldRun: when stopping, handleUnwatchedState inspects the
	// actual instance and sets False/Stopped directly. Without this guard, a
	// stopped VM with no watcher oscillates between False/Stopped and
	// Unknown/Reconciling on every reconcile triggered by the App controller's
	// Owns() watch.
	if shouldRun && phase == phaseUnknown && !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionUnknown, ReasonReconciling) {
		logger.Info("No watcher for instance, resetting Running condition to Unknown")
		if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionUnknown, ReasonReconciling, "Verifying instance state after controller restart"); err != nil {
			logger.Error(err, "Failed to reset Running condition")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if limaVM.Status.RestartNeeded {
		return r.handleRestartNeeded(ctx, limaVM, phase)
	}

	if phase != phaseUnknown {
		return r.handleWatchedState(ctx, limaVM, shouldRun, phase)
	}
	return r.handleUnwatchedState(ctx, limaVM, shouldRun)
}

// handleWatchedState handles a VM that has an active watcher reporting its phase.
func (r *LimaVMReconciler) handleWatchedState(ctx context.Context, limaVM *v1alpha1.LimaVM, shouldRun bool, phase instancePhase) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	switch {
	case phase == phaseStarting && shouldRun:
		// Watcher triggers the next reconcile when phase changes.
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStarting) {
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStarting, "Lima instance is starting"); err != nil {
				logger.Error(err, "Failed to update starting condition")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil

	case phase == phaseRunning && shouldRun:
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionTrue, ReasonStarted) {
			patch := client.MergeFrom(limaVM.DeepCopy())
			limaVM.Status.RestartCount++
			r.setCondition(ctx, limaVM, ConditionRunning, metav1.ConditionTrue, ReasonStarted, "Lima instance is running")
			if err := r.Status().Patch(ctx, limaVM, patch); err != nil {
				logger.Error(err, "Failed to update running condition")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil

	case phase == phaseStopped && shouldRun:
		// Hostagent exited while it should be running (crash or failed start).
		// Clean up the dead watcher and start fresh.
		r.stopWatcher(limaVM.Name)
		inst, err := store.Inspect(ctx, limaVM.Name)
		if err != nil {
			logger.Error(err, "Failed to inspect Lima instance")
			return ctrl.Result{}, err
		}
		if inst == nil {
			return ctrl.Result{}, errors.New("instance not found")
		}
		// The VM driver (e.g., QEMU) may outlive the hostagent. Force-stop the
		// instance so the next hostagent can start with a clean slate.
		// stopInstanceForcibly screens the on-disk HostAgentPID; the DriverPID it
		// cannot screen is still killed, so handleDeletion zeroes DriverPID on
		// Windows while a restart cannot — the orphaned driver must die. Job
		// Objects, not PID files, are the proper fix.
		if inst.Status == limatype.StatusRunning || inst.Status == limatype.StatusBroken {
			logger.Info("Force-stopping orphaned VM driver", "status", inst.Status)
			stopInstanceForcibly(ctx, logger, inst)
		}
		return r.startInstance(ctx, limaVM, inst)

	case phase == phaseRunning && !shouldRun:
		inst, err := store.Inspect(ctx, limaVM.Name)
		if err != nil {
			logger.Error(err, "Failed to inspect Lima instance")
			return ctrl.Result{}, err
		}
		if inst == nil {
			return ctrl.Result{}, errors.New("instance not found")
		}
		// TODO: Non-blocking stop: send SIGINT and return immediately;
		// the watcher detects the Exiting event and triggers a reconcile.
		return r.stopInstance(ctx, limaVM, inst)

	case phase == phaseStarting && !shouldRun:
		// Hostagent is alive and starting, but user wants it stopped.
		inst, err := store.Inspect(ctx, limaVM.Name)
		if err != nil {
			logger.Error(err, "Failed to inspect Lima instance")
			return ctrl.Result{}, err
		}
		if inst == nil {
			return ctrl.Result{}, errors.New("instance not found")
		}
		return r.stopInstance(ctx, limaVM, inst)

	default:
		// phase == phaseStopped && !shouldRun
		r.stopWatcher(limaVM.Name)
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStopped) {
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Lima instance is stopped"); err != nil {
				logger.Error(err, "Failed to update stopped condition")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
}

// handleUnwatchedState handles a VM with no active watcher. This occurs after
// controller restart. If a hostagent is still running, it is orphaned and must
// be killed so the next reconcile can start fresh with a watcher.
func (r *LimaVMReconciler) handleUnwatchedState(ctx context.Context, limaVM *v1alpha1.LimaVM, shouldRun bool) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	inst, err := store.Inspect(ctx, limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to inspect Lima instance")
		return ctrl.Result{}, err
	}
	if inst == nil {
		return ctrl.Result{}, errors.New("instance not found")
	}

	switch inst.Status {
	case limatype.StatusRunning, limatype.StatusBroken:
		// Orphaned hostagent from before controller restart. Kill it so the
		// next reconcile can start with a watcher.
		// killOrphanedHostagent guards the recycled HostAgentPID before signalling
		// or taskkill, as handleWatchedState does.
		logger.Info("Found orphaned hostagent, killing it", "status", inst.Status)
		if err := r.killOrphanedHostagent(ctx, inst); err != nil {
			logger.Error(err, "Failed to kill orphaned hostagent")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil

	default:
		// Stopped — proceed normally.
		if shouldRun {
			return r.startInstance(ctx, limaVM, inst)
		}
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStopped) {
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Lima instance is stopped"); err != nil {
				logger.Error(err, "Failed to update stopped condition")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}
}

// handleRestartNeeded acts on status.restartNeeded based on the watcher phase:
//   - Running: stop the instance and clear restartNeeded atomically.
//     The next reconcile starts it via normal shouldRun && !isRunning logic.
//   - Starting: return and let the watcher trigger the next reconcile.
//   - Stopped/Unknown: clear restartNeeded and fall through to normal logic.
func (r *LimaVMReconciler) handleRestartNeeded(ctx context.Context, limaVM *v1alpha1.LimaVM, phase instancePhase) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	switch phase {
	case phaseRunning:
		logger.Info("Restart needed: stopping running instance")
		r.shutdownHostagent(ctx, limaVM.Name, nil)
		r.stopWatcher(limaVM.Name)

		// Clear restartNeeded and set Stopped condition in one write.
		// This is inlined (rather than calling stopInstance) so both changes
		// land in a single patch — stopInstance's updateCondition would take
		// its own DeepCopy and miss the RestartNeeded change.
		patch := client.MergeFrom(limaVM.DeepCopy())
		limaVM.Status.RestartNeeded = false
		r.setCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Stopped for restart")
		return ctrl.Result{}, r.Status().Patch(ctx, limaVM, patch)

	case phaseStarting:
		// Watcher triggers next reconcile when phase changes.
		logger.Info("Restart needed but instance is starting, waiting for boot to complete")
		return ctrl.Result{}, nil

	default:
		// phaseStopped or phaseUnknown: clear flag and let normal logic handle it.
		logger.Info("Restart needed but instance not running, clearing flag", "phase", phase)
		patch := client.MergeFrom(limaVM.DeepCopy())
		limaVM.Status.RestartNeeded = false
		return ctrl.Result{}, r.Status().Patch(ctx, limaVM, patch)
	}
}

// wsl2RegistrationIsOrphaned reports whether inst is a WSL2 distro still
// registered with WSL but missing its root disk — a state Lima cannot recover
// on its own, because its WSL2 driver only re-imports a distro that is absent
// from `wsl --list`. It arises when an instance directory is removed without
// `wsl --unregister`. Always false off Windows, where VMType is never WSL2.
//
// inst.Status can read StatusRunning here even though no live process holds
// the disk open. The only caller that reaches this with StatusRunning
// (handleWatchedState's phaseStopped-but-shouldRun case) already ran
// stopInstanceForcibly, which terminates the WSL2 distro first. Preserve that
// ordering in any new startInstance caller, or this stat becomes a race.
func wsl2RegistrationIsOrphaned(inst *limatype.Instance) bool {
	if inst.VMType != limatype.WSL2 || inst.Status == limatype.StatusUninitialized {
		return false
	}
	_, err := os.Stat(filepath.Join(inst.Dir, wsl2RootDisk))
	return errors.Is(err, os.ErrNotExist)
}

// startInstance launches the hostagent and starts a watcher goroutine to track
// its lifecycle. The watcher triggers reconciles as the hostagent progresses
// through Starting → Running → Stopped, so no polling is needed.
//
// This duplicates much of Lima's own start logic because Lima's API blocks
// until the VM is fully running. We need to return immediately so the
// reconciler can handle other work while the VM boots.
func (r *LimaVMReconciler) startInstance(ctx context.Context, limaVM *v1alpha1.LimaVM, inst *limatype.Instance) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting Lima instance", "instance", limaVM.Name)

	// Record the Starting condition before any slow operations (waitForPIDFile
	// can block up to 5 seconds). This ensures the True→False status transition
	// is visible even if the object is modified externally during startup.
	if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStarting, "Lima instance is starting"); err != nil {
		logger.Error(err, "Failed to update starting condition")
		return ctrl.Result{}, err
	}

	// Heal an orphaned WSL2 registration before the hostagent boots it (see
	// wsl2RegistrationIsOrphaned). Unregistering makes Lima's Start (below, in
	// the hostagent) treat the distro as uninitialized and re-import basedisk,
	// instead of booting a missing disk into ERROR_FILE_NOT_FOUND forever.
	if wsl2RegistrationIsOrphaned(inst) {
		logger.Info("WSL2 distro is registered but its root disk is missing; unregistering to force re-import",
			"instance", limaVM.Name, "disk", wsl2RootDisk)
		if err := wsl.Unregister(ctx, wsl.DistroName(limaVM.Name)); err != nil {
			// Booting now would hit ERROR_FILE_NOT_FOUND again, so fail the start
			// and requeue to retry the unregister.
			logger.Error(err, "Failed to unregister orphaned WSL2 distro", "instance", limaVM.Name)
			if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
				logger.Error(updateErr, "Failed to update status after unregister failure")
			}
			return ctrl.Result{}, err
		}
	}

	// Get the path to our own executable (rdd) to use as the hostagent launcher
	rddPath, err := os.Executable()
	if err != nil {
		logger.Error(err, "Failed to get executable path")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after start failure")
		}
		return ctrl.Result{}, err
	}

	// Build hostagent command arguments
	haPIDPath := filepath.Join(inst.Dir, filenames.HostAgentPID)
	haSockPath := filepath.Join(inst.Dir, filenames.HostAgentSock)
	haStderrPath := filepath.Join(inst.Dir, filenames.HostAgentStderrLog)

	args := []string{
		"hostagent",
		"--pidfile", haPIDPath,
		"--socket", haSockPath,
	}
	if logger.V(1).Enabled() {
		args = append(args, "--debug")
	}
	args = append(args, inst.Name)

	// Create rotated log files. The active names (ha.stdout.log, ha.stderr.log)
	// match what Lima expects (e.g. StopForcibly, store.Inspect).
	keepLogs := os.Getenv("RDD_KEEP_LOGS") != ""
	title := os.Getenv("RDD_LOG_TITLE")
	var header string
	if title != "" {
		// JSONL format: Lima's event watcher parses it as a zero-value Event
		// and skips it; PropagateJSON logs it as a raw info line.
		b, _ := json.Marshal(struct {
			Title string `json:"title"`
		}{title})
		header = string(b) + "\n"
	}
	// Rotate serial logs before creating hostagent logs. The VM driver
	// overwrites serial.log on each start; rotating preserves previous boots.
	for _, name := range []string{"serial", "serialp", "serialv"} {
		if err := logfile.Rotate(inst.Dir, name, keepLogs); err != nil {
			logger.Error(err, "Failed to rotate serial log", "name", name)
		}
	}

	haStdoutW, err := logfile.Create(inst.Dir, "ha.stdout", keepLogs, header)
	if err != nil {
		logger.Error(err, "Failed to create stdout log file")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after log file creation failure")
		}
		return ctrl.Result{}, err
	}
	defer haStdoutW.Close()
	haStderrW, err := logfile.Create(inst.Dir, "ha.stderr", keepLogs, header)
	if err != nil {
		logger.Error(err, "Failed to create stderr log file")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after log file creation failure")
		}
		return ctrl.Result{}, err
	}
	defer haStderrW.Close()

	begin := time.Now()

	// SetGroup makes the hostagent a new process-group leader (pgid == pid
	// on Unix), which lets bats-with-timeout.sh attribute leaked qemu
	// grandchildren back to their hostagent ancestor via pgid.
	haCmd := exec.CommandContext(ctx, rddPath, args...)
	process.SetGroup(haCmd)
	haCmd.Stdout = haStdoutW
	haCmd.Stderr = haStderrW

	if err := haCmd.Start(); err != nil {
		logger.Error(err, "Failed to start hostagent")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after hostagent start failure")
		}
		return ctrl.Result{}, err
	}

	// Wait for PID file to be created (indicates hostagent has started)
	if err := r.waitForPIDFile(haPIDPath, haStderrPath); err != nil {
		logger.Error(err, "Hostagent did not start")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after hostagent startup failure")
		}
		return ctrl.Result{}, err
	}

	// Start watcher goroutine to track hostagent lifecycle and reap the process.
	// The watcher enqueues reconciles as phase transitions occur.
	r.startWatcher(ctx, limaVM.Name, limaVM.Namespace, haCmd, inst.Dir, begin)

	logger.Info("Hostagent started, watcher active", "instance", limaVM.Name, "pid", haCmd.Process.Pid)
	return ctrl.Result{}, nil
}

// waitForPIDFile waits for the hostagent PID file to be created.
func (r *LimaVMReconciler) waitForPIDFile(haPIDPath, haStderrPath string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(haPIDPath); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("hostagent did not create PID file within timeout (see " + haStderrPath + ")")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stopInstance stops the Lima VM and cleans up its watcher.
// TODO: Non-blocking stop: send SIGINT and return immediately;
// the watcher detects the Exiting event and triggers a reconcile.
func (r *LimaVMReconciler) stopInstance(ctx context.Context, limaVM *v1alpha1.LimaVM, inst *limatype.Instance) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Stopping Lima instance", "instance", limaVM.Name)

	r.shutdownHostagent(ctx, limaVM.Name, inst)
	r.stopWatcher(limaVM.Name)

	// Verify the instance stopped
	inst, err := store.Inspect(ctx, limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to inspect instance after stop")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after stop failure")
		}
		return ctrl.Result{}, err
	}

	if inst != nil && inst.Status == limatype.StatusRunning {
		err := errors.New("instance still running after stop attempt")
		logger.Error(err, "Failed to stop Lima instance")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after stop failure")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Stopped Lima instance", "instance", limaVM.Name)
	if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Lima instance stopped successfully"); err != nil {
		logger.Error(err, "Failed to update status after stop")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// shutdownHostagent stops the hostagent for the named instance: sends a graceful
// signal, waits for exit, and falls back to force-killing the process tree and
// cleaning up WSL2 distros and tmp files. If inst is nil, it is looked up via
// store.Inspect when needed for forceful cleanup.
func (r *LimaVMReconciler) shutdownHostagent(ctx context.Context, name string, inst *limatype.Instance) {
	logger := log.FromContext(ctx)

	forceStop := func() {
		// Use a background context: the parent reconciler context may be
		// nearing its deadline after the graceful shutdown wait.
		forceCtx, forceCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer forceCancel()
		forceInst := inst
		if forceInst == nil {
			var err error
			forceInst, err = store.Inspect(forceCtx, name)
			if err != nil {
				logger.Error(err, "Failed to inspect Lima instance for forceful stop")
				return
			}
			if forceInst == nil {
				return
			}
		}
		// forceStop runs after signalHostagent declines (no watcher, or the
		// process was already reaped) or after a signalled hostagent ignores the
		// graceful timeout. In the reaped case the stored HostAgentPID may already
		// be recycled on Windows; stopInstanceForcibly screens it before the
		// taskkill.
		stopInstanceForcibly(forceCtx, logger, forceInst)
	}

	// After forced termination, wait briefly for the process to exit.
	// Use a background context (like forceStop above) because the parent
	// reconciler context may be exhausted or cancelled by now.
	waitAfterKill := func() {
		killCtx, killCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer killCancel()
		r.waitForProcessExit(killCtx, name)
	}

	if r.signalHostagent(name) {
		stopCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
		defer cancel()
		// Not tested: the forceStop fallback requires a hostagent that ignores
		// shutdown signals. The orphaned-hostagent integration test exercises
		// forced stop indirectly but does not isolate this timeout path.
		if !r.waitForProcessExit(stopCtx, name) {
			logger.Info("Hostagent did not exit gracefully, forcing stop")
			forceStop()
			waitAfterKill()
		}
	} else {
		logger.Info("No watcher for hostagent, forcing stop via stored PIDs")
		forceStop()
		waitAfterKill()
	}
}

// killOrphanedHostagent terminates an orphaned hostagent (one running without
// a watcher, typically after a controller restart). It attempts graceful
// shutdown first by signaling the hostagent, giving it time to stop the VM
// driver and clean up. Falls back to forced termination after a timeout.
func (r *LimaVMReconciler) killOrphanedHostagent(ctx context.Context, inst *limatype.Instance) error {
	logger := log.FromContext(ctx)

	// Try graceful shutdown: signal the hostagent and wait for the instance
	// to become stopped. The hostagent's own shutdown sequence handles driver
	// termination, WSL2 distro cleanup, and tmp file removal.
	//
	// HostAgentPID comes from on-disk state written by a previous service, so on
	// Windows it may name a recycled process. Signal it only when IsOurProcess
	// confirms it is still our hostagent; stopInstanceForcibly re-screens before
	// the forced stop below, because the PID can be recycled during the graceful
	// wait.
	if process.IsOurProcess(process.HostagentInterruptKey(inst.Name), inst.HostAgentPID) {
		if err := process.Interrupt(process.HostagentInterruptKey(inst.Name), inst.HostAgentPID); err != nil {
			logger.V(1).Info("Could not signal orphaned hostagent", "pid", inst.HostAgentPID, "error", err)
		} else {
			stopCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
			defer cancel()
			if waitForInstanceStopped(stopCtx, inst.Name) {
				logger.Info("Orphaned hostagent exited gracefully")
				return nil
			}
			logger.Info("Orphaned hostagent did not exit gracefully, forcing stop")
		}
	}

	stopInstanceForcibly(ctx, logger, inst)
	return nil
}

// waitForInstanceStopped polls store.Inspect until the instance reports
// StatusStopped or the context is cancelled.
func waitForInstanceStopped(ctx context.Context, name string) bool {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			inst, err := store.Inspect(ctx, name)
			if err != nil {
				continue // Transient error; keep polling until context expires.
			}
			if inst == nil || inst.Status == limatype.StatusStopped {
				return true
			}
		}
	}
}

// clearRecycledHostAgentPID zeroes inst.HostAgentPID unless it still names our
// live hostagent, so a force-stop's taskkill cannot reach an unrelated process
// that recycled the PID. DriverPID is not screened — the qemu/wsl driver
// registers no interrupt event — so a recycled DriverPID is still taskkilled.
//
// TODO: track the hostagent and driver in a Windows Job Object so termination
// no longer trusts a stored DriverPID the OS may have recycled.
func clearRecycledHostAgentPID(inst *limatype.Instance) {
	if !process.IsOurProcess(process.HostagentInterruptKey(inst.Name), inst.HostAgentPID) {
		inst.HostAgentPID = 0
	}
}

// forceStopForDeletion stops a Lima instance in preparation for a forced Delete
// and, on Windows, clears its on-disk PIDs so Lima's Delete → StopForcibly
// cannot send CTRL_BREAK to a process that recycled a stale PID.
//
// Only Running instances are force-stopped by PID. Broken instances may have
// stale PID files pointing to recycled processes on Windows (Lima's ReadPIDFile
// treats any live PID as valid). Not tested: simulating stale PID files requires
// Windows-specific PID file manipulation that BATS cannot easily reproduce.
//
// Clearing the PIDs on Windows disables Lima's internal kill retry even if
// stopInstanceForcibly failed above. That is intentional: a failed kill means
// KillTree could not reach the process (access denied, already reaped), and the
// PID may already be recycled. Retrying with a stale PID is worse than letting
// Delete proceed without a kill. On Unix, PID recycling is rare (wraps around
// 32768+), so Lima's Delete may clean up any surviving driver processes.
func forceStopForDeletion(ctx context.Context, logger logr.Logger, inst *limatype.Instance) {
	if inst.Status == limatype.StatusRunning {
		// A StatusRunning WSL2 distro derives its status from wsl --list, not
		// from HostAgentPID, so that PID may have been recycled on Windows;
		// stopInstanceForcibly screens it before taskkill. For QEMU/VZ, Lima
		// reports StatusRunning only after the hostagent socket answers, so the
		// PID is genuine and IsOurProcess confirms it.
		stopInstanceForcibly(ctx, logger, inst)
	} else if inst.VMType == limatype.WSL2 {
		// A "stopped" WSL2 distro can retain kernel state that deadlocks
		// wsl.exe --unregister. Terminate it without PID-based killing, since
		// the PIDs may have been recycled on Windows.
		terminateWSL2Distro(ctx, logger, inst.Name)
	}
	if runtime.GOOS == "windows" {
		inst.DriverPID = 0
		inst.HostAgentPID = 0
	}
}

// stopInstanceForcibly terminates the hostagent and driver processes and their
// descendants. This replaces limainstance.StopForcibly because Lima's SysKill
// on Windows uses GenerateConsoleCtrlEvent(CTRL_BREAK) which targets the entire
// console group, killing the RDD service along with the hostagent.
//
// We use process.KillTree which sends SIGKILL to the process group on Unix and
// uses taskkill /F /T on Windows, ensuring child processes (e.g. ssh.exe port
// forwarders) are also terminated.
//
// On WSL2, also terminates the distro because the keepAlive process
// (nohup sleep) would keep it running after the hostagent is killed.
//
// It screens HostAgentPID with clearRecycledHostAgentPID before killing, so a
// recycled PID is never taskkilled. The screen runs on a copy, leaving the
// caller's instance unchanged.
func stopInstanceForcibly(ctx context.Context, logger logr.Logger, inst *limatype.Instance) {
	safeInst := *inst
	clearRecycledHostAgentPID(&safeInst)
	inst = &safeInst

	allKilled := true
	for _, pid := range []int{inst.DriverPID, inst.HostAgentPID} {
		if pid > 0 {
			if err := process.KillTree(ctx, pid); err != nil {
				logger.V(1).Info("Failed to kill process tree", "pid", pid, "error", err)
				allKilled = false
			}
		}
	}
	// Unlock additional disks only after confirming the instance is gone.
	// If KillTree failed, the VM driver may still be using the disks.
	if allKilled {
		for _, d := range inst.AdditionalDisks {
			disk, err := store.InspectDisk(d.Name, nil)
			if err != nil {
				logger.V(1).Info("Disk does not exist", "disk", d.Name)
				continue
			}
			if err := disk.Unlock(); err != nil {
				logger.V(1).Info("Failed to unlock disk", "disk", d.Name, "error", err)
			}
		}
	}
	// On WSL2, terminate the distro so store.Inspect reports StatusStopped.
	if inst.VMType == limatype.WSL2 {
		terminateWSL2Distro(ctx, logger, inst.Name)
	}
	// Clean up PID/socket/tmp files so the next hostagent can start cleanly.
	// Skip cleanup if any kill failed: Lima's store.Inspect derives StatusStopped
	// from missing PID files, so removing them would mask a still-running process.
	if !allKilled {
		logger.Info("Skipping tmp file cleanup because process kill failed")
		return
	}
	// Uses os.ReadDir (not filepath.Glob) because Glob treats brackets in the
	// path as meta-characters, silently failing on paths like C:\Users\name[1].
	entries, err := os.ReadDir(inst.Dir)
	if err != nil {
		logger.V(1).Info("Failed to read instance directory for cleanup", "dir", inst.Dir, "error", err)
		return
	}
	for _, f := range entries {
		for _, suffix := range filenames.TmpFileSuffixes {
			if strings.HasSuffix(f.Name(), suffix) {
				path := filepath.Join(inst.Dir, f.Name())
				if err := os.Remove(path); err != nil {
					logger.V(1).Info("Failed to remove tmp file", "path", path, "error", err)
				} else {
					logger.V(1).Info("Removed tmp file", "path", path)
				}
				break
			}
		}
	}
}

// terminateWSL2Distro terminates the WSL2 distro backing instName, logging any
// failure. No-op on non-Windows.
func terminateWSL2Distro(ctx context.Context, logger logr.Logger, instName string) {
	if err := wsl.Terminate(ctx, wsl.DistroName(instName)); err != nil {
		logger.V(1).Info("Failed to terminate WSL2 distro", "instance", instName, "error", err)
	}
}

// removeStaleInstance removes a stale Lima instance directory left behind by
// a previous service run whose cleanup failed. On Windows, the WSL2 distro is
// terminated and then unregistered (removing its ext4.vhdx and releasing any
// file locks) before the remaining Lima metadata directory is deleted; the WSL2
// calls are no-ops on other platforms, where the directory is removed directly.
func removeStaleInstance(ctx context.Context, logger logr.Logger, instName, instanceDir string) error {
	// Terminate the distro before unregistering it: wsl.exe --unregister can
	// deadlock on a distro that still holds kernel state, the same hazard
	// forceStopForDeletion guards against. Both calls are no-ops off Windows.
	terminateWSL2Distro(ctx, logger, instName)
	if err := wsl.Unregister(ctx, wsl.DistroName(instName)); err != nil {
		logger.Error(err, "Failed to unregister WSL2 distro; a stale registration may remain", "instance", instName)
	}
	return os.RemoveAll(instanceDir)
}

// preserveInstanceLogs moves log files from the Lima instance directory to
// a subdirectory of the service log directory before the instance is deleted.
// This is a no-op unless RDD_KEEP_LOGS is set.
//
// Errors are logged but do not prevent deletion. On Windows, os.Rename
// requires FILE_SHARE_DELETE on the source; Go sets this flag since 1.14,
// but non-Go processes (e.g., QEMU) may not. If rename fails because a
// process still holds a lock, the logs are lost when the instance directory
// is deleted afterward.
func preserveInstanceLogs(ctx context.Context, inst *limatype.Instance) {
	if os.Getenv("RDD_KEEP_LOGS") == "" {
		return
	}

	logger := log.FromContext(ctx)
	count, err := instance.PreserveLogs(inst.Dir, inst.Name)
	if err != nil {
		logger.Error(err, "Failed to preserve instance logs")
		return
	}
	if count > 0 {
		logger.Info("Preserved instance logs", "instance", inst.Name, "count", count)
	}
}
