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
	"time"

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
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/logfile"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
)

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
		// Force-stop the instance if running. No graceful shutdown needed
		// since the instance is being deleted; users who want a graceful
		// shutdown can stop the instance before deleting.
		if existingInst.Status == limatype.StatusRunning {
			logger.Info("Force-stopping Lima instance before deletion", "instance", limaVM.Name)
			limainstance.StopForcibly(existingInst)
		}
		logger.Info("Deleting Lima instance", "instance", limaVM.Name)
		if err := limainstance.Delete(ctx, existingInst, true); err != nil {
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
	if phase == phaseUnknown && !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionUnknown, ReasonReconciling) {
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
			r.setCondition(limaVM, ConditionRunning, metav1.ConditionTrue, ReasonStarted, "Lima instance is running")
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
		// The VM driver (e.g., QEMU) may outlive the hostagent. Force-stop
		// the instance so the next hostagent can start with a clean slate.
		if inst.Status == limatype.StatusRunning || inst.Status == limatype.StatusBroken {
			logger.Info("Force-stopping orphaned VM driver", "status", inst.Status)
			limainstance.StopForcibly(inst)
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

		r.signalHostagent(limaVM.Name, os.Interrupt)
		stopCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
		defer cancel()
		if !r.waitForProcessExit(stopCtx, limaVM.Name) {
			logger.Info("Hostagent did not exit gracefully, forcing stop")
			inst, err := store.Inspect(ctx, limaVM.Name)
			if err != nil {
				logger.Error(err, "Failed to inspect Lima instance for forceful stop")
			} else if inst != nil {
				limainstance.StopForcibly(inst)
			}
			r.waitForProcessExit(ctx, limaVM.Name)
		}

		r.stopWatcher(limaVM.Name)

		// Clear restartNeeded and set Stopped condition in one write.
		// This is inlined (rather than calling stopInstance) so both changes
		// land in a single patch — stopInstance's updateCondition would take
		// its own DeepCopy and miss the RestartNeeded change.
		patch := client.MergeFrom(limaVM.DeepCopy())
		limaVM.Status.RestartNeeded = false
		r.setCondition(limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Stopped for restart")
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

	// Start hostagent in background.
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

	logger.Info("Hostagent started, watcher active", "instance", limaVM.Name)
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

	r.signalHostagent(limaVM.Name, os.Interrupt)
	stopCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
	defer cancel()
	if !r.waitForProcessExit(stopCtx, limaVM.Name) {
		logger.Info("Hostagent did not exit gracefully, forcing stop")
		limainstance.StopForcibly(inst)
		r.waitForProcessExit(ctx, limaVM.Name)
	}

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

// killOrphanedHostagent terminates an orphaned hostagent (one running without a
// watcher, typically after a controller restart). Sends SIGTERM first and falls
// back to SIGKILL after the graceful shutdown timeout.
func (r *LimaVMReconciler) killOrphanedHostagent(ctx context.Context, inst *limatype.Instance) error {
	stopCtx, cancel := context.WithTimeout(ctx, gracefulShutdownTimeout)
	defer cancel()
	if err := limainstance.StopGracefully(stopCtx, inst, false); err != nil {
		limainstance.StopForcibly(inst)
	}
	return nil
}
