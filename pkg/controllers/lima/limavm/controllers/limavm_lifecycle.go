// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	limainstance "github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/limatype"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/store"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
)

func (r *LimaVMReconciler) handleDeletion(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Stop and delete the Lima instance.
	// Inspect may fail if the instance doesn't exist, which is fine during
	// deletion - we just proceed to remove the finalizer.
	existingInst, err := store.Inspect(ctx, limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to inspect Lima instance for deletion")
	}
	if existingInst != nil {
		// Stop the instance if running
		if existingInst.Status == limatype.StatusRunning {
			logger.Info("Stopping Lima instance before deletion", "instance", limaVM.Name)
			if err := limainstance.StopGracefully(ctx, existingInst, false); err != nil {
				logger.Error(err, "Failed to stop Lima instance gracefully, forcing stop")
				limainstance.StopForcibly(existingInst)
			}
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
	if err := base.RemoveFinalizer(ctx, r.Client, limaVM); err != nil {
		logger.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	logger.Info("Deleted Lima instance, owned resources, and finalizer")
	return ctrl.Result{}, nil
}

// handleRunningState manages VM start/stop based on spec.running.
func (r *LimaVMReconciler) handleRunningState(ctx context.Context, limaVM *v1alpha1.LimaVM) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Get current instance status
	inst, err := store.Inspect(ctx, limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to inspect Lima instance")
		return ctrl.Result{}, err
	}
	if inst == nil {
		// Instance doesn't exist - shouldn't happen if Created is True
		logger.Error(nil, "Lima instance not found despite Created condition")
		return ctrl.Result{}, errors.New("instance not found")
	}

	// Handle broken state. Lima reports "broken" when the hostagent PID file
	// exists but the socket is not yet responding. During startup this is normal:
	// the hostagent creates its PID file before the socket. If the hostagent
	// process is alive and the socket doesn't exist yet, treat it as starting.
	if inst.Status == limatype.StatusBroken {
		haSockPath := filepath.Join(inst.Dir, filenames.HostAgentSock)
		if inst.HostAgentPID > 0 && !fileExists(haSockPath) {
			logger.Info("Instance reports broken but hostagent is alive and socket not yet created, treating as starting", "pid", inst.HostAgentPID)
			if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStarting) {
				if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStarting, "Lima instance is starting"); err != nil {
					logger.Error(err, "Failed to update starting condition")
					return ctrl.Result{}, err
				}
			}
			// Poll until the hostagent finishes startup and the socket appears.
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}

		logger.Info("Lima instance is broken, attempting force stop to recover", "errors", inst.Errors)
		limainstance.StopForcibly(inst)

		// Re-inspect to see if recovery succeeded
		inst, err = store.Inspect(ctx, limaVM.Name)
		if err != nil {
			logger.Error(err, "Failed to inspect Lima instance after force stop")
			return ctrl.Result{}, err
		}

		if inst.Status == limatype.StatusBroken {
			errMsg := "Lima instance is in broken state"
			if len(inst.Errors) > 0 {
				errMsg = inst.Errors[0].Error()
			}
			logger.Error(nil, "Failed to recover broken Lima instance", "errors", inst.Errors)
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonBroken, errMsg); err != nil {
				logger.Error(err, "Failed to update broken condition")
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, errors.New(errMsg)
		}

		logger.Info("Recovered broken Lima instance via force stop", "newStatus", inst.Status)
		if r.Recorder != nil {
			r.Recorder.Eventf(limaVM, nil, corev1.EventTypeWarning, "BrokenStateRecovered", "Recovery",
				"Recovered from broken state via force stop")
		}
	}

	shouldRun := limaVM.Spec.Running
	isRunning := inst.Status == limatype.StatusRunning

	logger.Info("Checking running state", "shouldRun", shouldRun, "isRunning", isRunning, "status", inst.Status)

	if shouldRun && !isRunning {
		// Check if hostagent is already starting (PID file exists)
		haPIDPath := filepath.Join(inst.Dir, filenames.HostAgentPID)
		if _, err := os.Stat(haPIDPath); err == nil {
			// PID file exists, hostagent is starting
			logger.Info("Lima instance is starting, waiting for it to be running")
			if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStarting) {
				if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStarting, "Lima instance is starting"); err != nil {
					logger.Error(err, "Failed to update starting condition")
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return r.startInstance(ctx, limaVM, inst)
	}
	if !shouldRun && isRunning {
		return r.stopInstance(ctx, limaVM, inst)
	}

	// Update condition to reflect current state (including reason, so StartFailed/Starting gets updated)
	if isRunning {
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionTrue, ReasonStarted) {
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionTrue, ReasonStarted, "Lima instance is running"); err != nil {
				logger.Error(err, "Failed to update running condition")
				return ctrl.Result{}, err
			}
		}
	} else {
		if !base.HasConditionWithReason(limaVM.Status.Conditions, ConditionRunning, metav1.ConditionFalse, ReasonStopped) {
			if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStopped, "Lima instance is stopped"); err != nil {
				logger.Error(err, "Failed to update running condition")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// startInstance starts the Lima VM hostagent in the background.
// It returns immediately after launching the hostagent, without waiting for
// the VM to be fully running. The reconciler will be requeued to check the
// running state later.
//
// This duplicates much of Lima's own start logic because Lima's API blocks
// until the VM is fully running. We need to return immediately so the
// reconciler can handle other work while the VM boots.
func (r *LimaVMReconciler) startInstance(ctx context.Context, limaVM *v1alpha1.LimaVM, inst *limatype.Instance) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting Lima instance", "instance", limaVM.Name)

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
	haStdoutPath := filepath.Join(inst.Dir, filenames.HostAgentStdoutLog)
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

	// Create log files for hostagent output (os.Create truncates existing files)
	haStdoutW, err := os.Create(haStdoutPath)
	if err != nil {
		logger.Error(err, "Failed to create stdout log file")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after log file creation failure")
		}
		return ctrl.Result{}, err
	}
	defer haStdoutW.Close()
	haStderrW, err := os.Create(haStderrPath)
	if err != nil {
		logger.Error(err, "Failed to create stderr log file")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after log file creation failure")
		}
		return ctrl.Result{}, err
	}
	defer haStderrW.Close()
	// Start hostagent in background
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

	// Reap the hostagent process when it exits. Without this, the hostagent
	// becomes a zombie, and Lima's kill(pid, 0) check thinks it is still alive.
	go func() {
		if err := haCmd.Wait(); err != nil {
			logger.Error(err, "Hostagent process exited with error")
		}
	}()

	// Wait for PID file to be created (indicates hostagent has started)
	if err := r.waitForPIDFile(haPIDPath, haStderrPath); err != nil {
		logger.Error(err, "Hostagent did not start")
		if updateErr := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStartFailed, err.Error()); updateErr != nil {
			logger.Error(updateErr, "Failed to update status after hostagent startup failure")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Hostagent started, waiting for instance to be running", "instance", limaVM.Name)
	if err := r.updateCondition(ctx, limaVM, ConditionRunning, metav1.ConditionFalse, ReasonStarting, "Lima instance is starting"); err != nil {
		logger.Error(err, "Failed to update status after hostagent start")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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

// stopInstance stops the Lima VM.
func (r *LimaVMReconciler) stopInstance(ctx context.Context, limaVM *v1alpha1.LimaVM, inst *limatype.Instance) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Stopping Lima instance", "instance", limaVM.Name)

	if err := limainstance.StopGracefully(ctx, inst, false); err != nil {
		logger.Error(err, "Failed to stop Lima instance gracefully")
		// Try forceful stop
		logger.Info("Attempting forceful stop")
		limainstance.StopForcibly(inst)
	}

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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
