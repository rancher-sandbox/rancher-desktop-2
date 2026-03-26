// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	limainstance "github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
	"github.com/lima-vm/lima/v2/pkg/store"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
)

const (
	// TemplateConfigMapLabel is the label applied to template ConfigMaps managed by LimaVM resources.
	TemplateConfigMapLabel = "lima.rancherdesktop.io/template-configmap"

	// ConditionCreated indicates whether the Lima instance has been created on disk.
	ConditionCreated = "Created"

	// ConditionRunning indicates whether the Lima instance is running.
	ConditionRunning = "Running"

	// ReasonCreated is used when the Lima instance was successfully created.
	ReasonCreated = "Created"

	// ReasonCreateFailed is used when the Lima instance creation failed.
	ReasonCreateFailed = "CreateFailed"

	// ReasonPending is used when the reconciler has seen the resource but not yet created the instance.
	ReasonPending = "Pending"

	// ReasonStarting is used when the Lima instance is starting but not yet running.
	ReasonStarting = "Starting"

	// ReasonStarted is used when the Lima instance was successfully started.
	ReasonStarted = "Started"

	// ReasonStartFailed is used when the Lima instance failed to start.
	ReasonStartFailed = "StartFailed"

	// ReasonStopped is used when the Lima instance was successfully stopped.
	ReasonStopped = "Stopped"

	// ReasonStopFailed is used when the Lima instance failed to stop.
	ReasonStopFailed = "StopFailed"

	// ReasonReconciling is used when the controller has restarted and
	// the Running condition has not yet been verified.
	ReasonReconciling = "Reconciling"

	// preparingSentinel is a marker file created during instance preparation.
	// Its presence indicates that preparation is in progress or failed.
	preparingSentinel = ".preparing"

	// gracefulShutdownTimeout is the time to wait for a hostagent to exit
	// after requesting graceful shutdown before falling back to forced termination.
	gracefulShutdownTimeout = 30 * time.Second
)

// sentinelPath returns the path to the preparing sentinel file for an instance.
func sentinelPath(instanceName string) string {
	return filepath.Join(instance.LimaHome(), instanceName, preparingSentinel)
}

// hasSentinel reports whether the preparing sentinel file exists.
func hasSentinel(instanceName string) bool {
	_, err := os.Stat(sentinelPath(instanceName))
	return err == nil
}

// createSentinel creates the preparing sentinel file.
func createSentinel(instanceName string) error {
	return os.WriteFile(sentinelPath(instanceName), nil, 0o644)
}

// removeSentinel removes the preparing sentinel file.
func removeSentinel(instanceName string) error {
	err := os.Remove(sentinelPath(instanceName))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// instanceTemplatePath returns the path to the lima.yaml for an instance.
func instanceTemplatePath(instanceName string) string {
	return filepath.Join(instance.LimaHome(), instanceName, filenames.LimaYAML)
}

// readInstanceTemplate reads the lima.yaml from the instance directory.
func readInstanceTemplate(instanceName string) ([]byte, error) {
	return os.ReadFile(instanceTemplatePath(instanceName))
}

// writeInstanceTemplate writes the lima.yaml to the instance directory.
func writeInstanceTemplate(instanceName string, data []byte) error {
	return os.WriteFile(instanceTemplatePath(instanceName), data, 0o644)
}

// LimaVMReconciler reconciles a LimaVM object.
type LimaVMReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Manager  ctrl.Manager
	Recorder events.EventRecorder

	instanceStatesMu sync.RWMutex
	instanceStates   map[string]*instanceState
	reconcileChan    chan event.TypedGenericEvent[*v1alpha1.LimaVM]
}

// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lima.rancherdesktop.io,resources=limavms/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile moves the cluster state toward the desired state.
// See docs/design/api_lima.md for a flow diagram.
//
// Webhook and reconciler responsibilities:
// - Mutating webhook: adds finalizer, validates templateRef, creates ConfigMap
// - Reconciler: sets owner reference (needs LimaVM UID, available only after persistence)
// - Reconciler: sets status.templateConfigMap, creates Lima instance on disk
// - ConfigMap webhook: validates template content, prevents deletion
//
// The status.templateConfigMap field tells consumers which ConfigMap holds the template
// and signals that owner reference setup is complete.
//
// The templateRef field documents the template's origin; the webhook handles it at creation.
func (r *LimaVMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var limaVM v1alpha1.LimaVM
	if err := r.Get(ctx, req.NamespacedName, &limaVM); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("LimaVM resource not found; already deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get LimaVM")
		return ctrl.Result{}, err
	}

	if base.IsBeingDeleted(&limaVM) {
		return r.handleDeletion(ctx, &limaVM)
	}

	// Set initial condition to Unknown so other components know reconciliation is in progress.
	if apimeta.FindStatusCondition(limaVM.Status.Conditions, ConditionCreated) == nil {
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionUnknown, ReasonPending, "Reconciliation in progress")
		if err := r.Status().Update(ctx, &limaVM); err != nil {
			logger.Error(err, "Failed to set initial condition")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Handle instances with a preparing sentinel file. The sentinel indicates that
	// a previous reconcile started preparation but didn't complete successfully.
	if hasSentinel(limaVM.Name) {
		if apimeta.IsStatusConditionTrue(limaVM.Status.Conditions, ConditionCreated) {
			// Preparation completed but sentinel wasn't cleaned up; remove it now.
			if err := removeSentinel(limaVM.Name); err != nil {
				logger.Error(err, "Failed to remove sentinel file")
				return ctrl.Result{}, err
			}
			logger.Info("Removed stale sentinel file", "instance", limaVM.Name)
			// Continue to check for further work.
		} else {
			// Preparation didn't complete; delete the instance directory.
			// Use os.RemoveAll because store.Inspect may fail if lima.yaml is missing.
			instanceDir := filepath.Join(instance.LimaHome(), limaVM.Name)
			logger.Info("Deleting incomplete instance directory", "path", instanceDir)
			if err := os.RemoveAll(instanceDir); err != nil {
				logger.Error(err, "Failed to delete incomplete instance directory")
				return ctrl.Result{}, err
			}
		}
		// Requeue to continue with fresh state after cleanup.
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	// Delete any leftover instance from a failed deletion before setting up owner references.
	if limaVM.Status.TemplateConfigMap == "" {
		existingInst, err := store.Inspect(ctx, limaVM.Name)
		if err == nil && existingInst != nil {
			logger.Info("Deleting leftover Lima instance", "instance", limaVM.Name)
			if err := limainstance.Delete(ctx, existingInst, true); err != nil {
				logger.Error(err, "Failed to delete leftover Lima instance")
				return ctrl.Result{}, err
			}
		}
	}

	// Get the template ConfigMap (created by the mutating webhook)
	configMapName := limaVM.GetTemplateConfigMapName()
	templateConfigMap := &corev1.ConfigMap{}
	configMapKey := types.NamespacedName{Name: configMapName, Namespace: limaVM.Namespace}
	if err := r.Get(ctx, configMapKey, templateConfigMap); err != nil {
		logger.Error(err, "Failed to get template ConfigMap")
		return ctrl.Result{}, err
	}

	// Set owner reference (the webhook already set the finalizer)
	if !base.IsOwnedByUID(templateConfigMap, limaVM.GetUID()) {
		if err := ctrl.SetControllerReference(&limaVM, templateConfigMap, r.Scheme); err != nil {
			logger.Error(err, "Failed to set owner reference on template ConfigMap")
			return ctrl.Result{}, err
		}
		if err := r.Update(ctx, templateConfigMap); err != nil {
			logger.Error(err, "Failed to update template ConfigMap")
			return ctrl.Result{}, err
		}
		logger.Info("Set owner reference on template ConfigMap", "ConfigMap.Name", configMapName)
		return ctrl.Result{}, nil
	}

	// Record the template ConfigMap name in status
	if limaVM.Status.TemplateConfigMap == "" {
		limaVM.Status.TemplateConfigMap = configMapName
		if err := r.Status().Update(ctx, &limaVM); err != nil {
			logger.Error(err, "Failed to update status.templateConfigMap")
			return ctrl.Result{}, err
		}
		logger.Info("Set status.templateConfigMap", "ConfigMap.Name", configMapName)
		return ctrl.Result{}, nil
	}

	// Instance already created - handle restart annotation, template changes, then running state.
	if apimeta.IsStatusConditionTrue(limaVM.Status.Conditions, ConditionCreated) {
		if _, hasAnnotation := limaVM.Annotations[v1alpha1.AnnotationRestartRequested]; hasAnnotation {
			return r.handleRestartAnnotation(ctx, &limaVM)
		}
		if templateConfigMap.ResourceVersion != limaVM.Status.ObservedTemplateResourceVersion {
			result, err := r.handleTemplateUpdate(ctx, &limaVM, templateConfigMap)
			if err != nil || !result.IsZero() {
				return result, err
			}
		}
		return r.handleRunningState(ctx, &limaVM)
	}

	// Instance exists on disk (perhaps from a previous reconcile); record the condition
	// and return to let the next reconcile handle running state (one mutation per reconcile).
	existingInst, err := store.Inspect(ctx, limaVM.Name)
	if err == nil && existingInst != nil {
		logger.Info("Lima instance already exists", "instance", limaVM.Name)
		limaVM.Status.ObservedTemplateResourceVersion = templateConfigMap.ResourceVersion
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance exists")
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status for existing instance")
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// Extract template data
	templateData, ok := templateConfigMap.Data[v1alpha1.TemplateConfigMapKey]
	if !ok || templateData == "" {
		err := errors.New("template ConfigMap missing template data")
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Create the Lima instance
	inst, err := limainstance.Create(ctx, limaVM.Name, []byte(templateData), false)
	if err != nil {
		logger.Error(err, "Failed to create Lima instance")
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after instance creation failure")
		}
		return ctrl.Result{}, err
	}

	// Create sentinel file to mark preparation in progress.
	if err := createSentinel(limaVM.Name); err != nil {
		logger.Error(err, "Failed to create sentinel file")
		if delErr := limainstance.Delete(ctx, inst, true); delErr != nil {
			logger.Error(delErr, "Failed to clean up instance after sentinel creation failure")
		}
		return ctrl.Result{}, err
	}

	// Prepare the instance: download images and create disks.
	// The guestAgent path is a placeholder during Prepare (only stored for later);
	// limactl start will look up the real path when called.
	if _, err := limainstance.Prepare(ctx, inst, "placeholder"); err != nil {
		logger.Error(err, "Failed to prepare Lima instance")
		// Clean up the partially created instance so the next reconcile doesn't
		// see it as existing and skip creation.
		if delErr := limainstance.Delete(ctx, inst, true); delErr != nil {
			logger.Error(delErr, "Failed to clean up instance after prepare failure")
		}
		r.setCondition(&limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after instance preparation failure")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Created Lima instance", "instance", limaVM.Name)
	limaVM.Status.ObservedTemplateResourceVersion = templateConfigMap.ResourceVersion
	r.setCondition(&limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance created successfully")
	if err := r.Status().Update(ctx, &limaVM); err != nil {
		logger.Error(err, "Failed to update status after instance creation")
		return ctrl.Result{}, err
	}

	// Remove sentinel file now that preparation is complete and status is updated.
	if err := removeSentinel(limaVM.Name); err != nil {
		logger.Error(err, "Failed to remove sentinel file after successful creation")
		return ctrl.Result{}, err
	}

	// Return and let the next reconcile handle running state (one mutation per reconcile)
	return ctrl.Result{}, nil
}

// handleTemplateUpdate detects and applies changes to the template ConfigMap.
// If the on-disk lima.yaml differs from the ConfigMap, it writes the new template.
// For running instances, it sets status.restartNeeded so the existing restart
// machinery handles the stop/start cycle.
//
// When a restart is pending, the method defers the observedTemplateResourceVersion
// update until after the restart completes. This prevents a race where observers
// see the new version while Running=True still reflects the pre-restart state.
// The next reconcile re-enters this method (stale observed version), finds the
// on-disk template identical, and records the version then.
//
// All paths return an empty result so the caller falls through to
// handleRunningState.
func (r *LimaVMReconciler) handleTemplateUpdate(ctx context.Context, limaVM *v1alpha1.LimaVM, templateConfigMap *corev1.ConfigMap) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	diskTemplate, err := readInstanceTemplate(limaVM.Name)
	if err != nil {
		logger.Error(err, "Failed to read on-disk template")
		return ctrl.Result{}, err
	}

	newTemplate := templateConfigMap.Data[v1alpha1.TemplateConfigMapKey]

	if string(diskTemplate) == newTemplate {
		if limaVM.Status.RestartNeeded {
			// Defer the version update until after the restart completes.
			return ctrl.Result{}, nil
		}
		// A ConfigMap update that doesn't change the template key (e.g. label
		// change) still bumps resourceVersion; recording it avoids repeated
		// comparisons on subsequent reconciles.
		logger.Info("Template ConfigMap changed but on-disk template is identical")
		limaVM.Status.ObservedTemplateResourceVersion = templateConfigMap.ResourceVersion
		return ctrl.Result{}, r.Status().Update(ctx, limaVM)
	}

	phase := r.getInstancePhase(limaVM.Name)
	if phase == phaseRunning || phase == phaseStarting {
		// For running instances: set restartNeeded before writing to disk.
		// If this status update fails, nothing else changed and the next
		// reconcile retries. If the subsequent disk write fails, restartNeeded
		// is already set and the observed version is still stale, so the next
		// reconcile re-enters this method and retries the disk write.
		if !limaVM.Status.RestartNeeded {
			logger.Info("Instance running with stale template, requesting restart")
			patch := client.MergeFrom(limaVM.DeepCopy())
			limaVM.Status.RestartNeeded = true
			if err := r.Status().Patch(ctx, limaVM, patch); err != nil {
				logger.Error(err, "Failed to set restartNeeded after template change")
				return ctrl.Result{}, err
			}
		}
	}

	logger.Info("Template changed, updating on-disk lima.yaml")
	if err := writeInstanceTemplate(limaVM.Name, []byte(newTemplate)); err != nil {
		logger.Error(err, "Failed to write updated template to disk")
		return ctrl.Result{}, err
	}

	if limaVM.Status.RestartNeeded {
		// Defer the version update until after the restart completes.
		return ctrl.Result{}, nil
	}

	// Persist observedTemplateResourceVersion after the disk write succeeds.
	patch := client.MergeFrom(limaVM.DeepCopy())
	limaVM.Status.ObservedTemplateResourceVersion = templateConfigMap.ResourceVersion
	if err := r.Status().Patch(ctx, limaVM, patch); err != nil {
		logger.Error(err, "Failed to update observedTemplateResourceVersion")
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setCondition updates or adds a condition in the LimaVM status.
func (r *LimaVMReconciler) setCondition(limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) {
	changed := apimeta.SetStatusCondition(&limaVM.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: base.TruncateConditionMessage(message),
	})
	if changed && r.Recorder != nil {
		r.Recorder.Eventf(limaVM, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
	}
}

// updateCondition sets a condition and patches the status subresource.
func (r *LimaVMReconciler) updateCondition(ctx context.Context, limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Patch (not Update) avoids resource version conflicts when the object is
	// modified externally during long-running operations like waitForPIDFile or
	// graceful shutdown.
	patch := client.MergeFrom(limaVM.DeepCopy())
	r.setCondition(limaVM, conditionType, status, reason, message)
	return r.Status().Patch(ctx, limaVM, patch)
}

// SetupWithManager sets up the controller with the Manager.
func (r *LimaVMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// No parent context exists yet; the manager hasn't started.
	if err := ensureSSHKeys(context.Background()); err != nil {
		return fmt.Errorf("failed to generate Lima SSH keys: %w", err)
	}

	r.instanceStates = make(map[string]*instanceState)
	r.reconcileChan = make(chan event.TypedGenericEvent[*v1alpha1.LimaVM], 1)

	if err := mgr.Add(manager.RunnableFunc(r.waitForShutdown)); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.LimaVM{}).
		Owns(&corev1.ConfigMap{}).
		WatchesRawSource(source.Channel(
			r.reconcileChan,
			&handler.TypedEnqueueRequestForObject[*v1alpha1.LimaVM]{},
		)).
		Complete(r)
}

// waitForShutdown blocks until the manager context is cancelled, then
// terminates all running hostagents.
func (r *LimaVMReconciler) waitForShutdown(ctx context.Context) error {
	<-ctx.Done()
	r.shutdownAllHostagents()
	return nil
}

// shutdownAllHostagents terminates all running hostagents during graceful shutdown.
// It sends a graceful shutdown signal to each hostagent, waits for exit, and falls
// back to process termination after a timeout.
func (r *LimaVMReconciler) shutdownAllHostagents() {
	r.instanceStatesMu.RLock()
	states := maps.Clone(r.instanceStates)
	r.instanceStatesMu.RUnlock()

	if len(states) == 0 {
		return
	}

	// Send graceful shutdown signal to all hostagents in parallel.
	for _, state := range states {
		if state.cmd != nil && state.cmd.Process != nil {
			_ = process.Interrupt(state.cmd.Process.Pid)
		}
	}

	// Wait for each hostagent process to exit. The watcher goroutine may finish
	// before the process (events.Watch returns on context cancel), so we wait on
	// procExited (closed by haCmd.Wait) rather than done (closed by the watcher).
	// TODO: Wait on all hostagents in parallel instead of sequentially; with the
	// current loop, the total wait is N × gracefulShutdownTimeout in the worst case.
	for name, state := range states {
		graceful := true
		select {
		case <-state.procExited:
		case <-time.After(gracefulShutdownTimeout):
			graceful = false
		}
		if !graceful {
			// Manager context is cancelled; use a fresh context for cleanup.
			// Inspect before killing so PIDs are still valid (not yet recycled
			// to unrelated processes). stopInstanceForcibly kills the process
			// tree and cleans up PID/socket/tmp files in one pass.
			logger := ctrl.Log.WithName("shutdownAllHostagents")
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			inst, err := store.Inspect(cleanupCtx, name)
			if err == nil && inst != nil {
				stopInstanceForcibly(cleanupCtx, logger, inst)
			}
			cancel()
			// Wait briefly for the process to exit after forced kill.
			// Without a timeout, cmd.Wait can block indefinitely if a
			// child process survives KillTree and holds an inherited pipe.
			waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
			select {
			case <-state.procExited:
			case <-waitCtx.Done():
				logger.Info("Timed out waiting for hostagent to exit after forced kill", "instance", name)
			}
			waitCancel()
		}
		state.cancel()
		r.instanceStatesMu.Lock()
		delete(r.instanceStates, name)
		r.instanceStatesMu.Unlock()
	}
}
