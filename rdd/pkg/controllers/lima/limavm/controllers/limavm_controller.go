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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lima-vm/lima/v2/pkg/fileutils"
	limainstance "github.com/lima-vm/lima/v2/pkg/instance"
	"github.com/lima-vm/lima/v2/pkg/limatype"
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
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/xz"
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
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionUnknown, ReasonPending, "Reconciliation in progress")
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
			forceStopForDeletion(ctx, logger, existingInst)
			// Use a timeout because Lima's WSL2 driver calls wsl.exe --unregister
			// which can hang indefinitely if the WSL subsystem is degraded.
			deleteCtx, deleteCancel := context.WithTimeout(ctx, time.Minute)
			err := limainstance.Delete(deleteCtx, existingInst, true)
			deleteCancel()
			if err != nil {
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
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance exists")
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
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Create the Lima instance
	inst, err := limainstance.Create(ctx, limaVM.Name, []byte(templateData), false)
	if err != nil {
		// If the directory exists but store.Inspect returned nil above (no valid
		// Lima metadata), the directory is stale — left behind by a previous
		// service run whose cleanup failed (e.g. file locks from orphaned WSL2
		// processes on Windows). removeStaleInstance handles the platform-specific
		// cleanup (unregistering the WSL2 distro on Windows to release the VHD
		// lock) before removing the directory, then we requeue to start fresh.
		if strings.Contains(err.Error(), "already exists") {
			instanceDir := filepath.Join(instance.LimaHome(), limaVM.Name)
			logger.Info("Found stale Lima instance directory, removing it", "path", instanceDir)
			if rmErr := removeStaleInstance(ctx, logger, limaVM.Name, instanceDir); rmErr != nil {
				logger.Error(rmErr, "Failed to remove stale Lima instance directory; will retry")
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		logger.Error(err, "Failed to create Lima instance")
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
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

	// When no system xz is on PATH, decompress the image in-process; Prepare
	// then finds the ready image and skips its own xz shell-out.
	if err := ensureImageDecompressed(ctx, inst); err != nil {
		logger.Error(err, "Failed to decompress instance image")
		if delErr := limainstance.Delete(ctx, inst, true); delErr != nil {
			logger.Error(delErr, "Failed to clean up instance after decompression failure")
		}
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after image decompression failure")
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
		r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionFalse, ReasonCreateFailed, err.Error())
		if statusErr := r.Status().Update(ctx, &limaVM); statusErr != nil {
			logger.Error(statusErr, "Failed to update status after instance preparation failure")
		}
		return ctrl.Result{}, err
	}

	logger.Info("Created Lima instance", "instance", limaVM.Name)
	// Patch (not Update) because Prepare() above can run for several minutes
	// (image download + decompression), during which the app controller may
	// propagate spec.running and bump resourceVersion. A failed Update here
	// would leave the sentinel in place and cause the next reconcile to
	// delete the just-prepared instance directory.
	patch := client.MergeFrom(limaVM.DeepCopy())
	limaVM.Status.ObservedTemplateResourceVersion = templateConfigMap.ResourceVersion
	r.setCondition(ctx, &limaVM, ConditionCreated, metav1.ConditionTrue, ReasonCreated, "Lima instance created successfully")
	if err := r.Status().Patch(ctx, &limaVM, patch); err != nil {
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

// ensureImageDecompressed pre-produces the decompressed distro image with the
// in-process xz decoder when no system xz is on PATH, so provisioning works on a
// host without xz (base macOS, a clean Windows). When xz is present rdd leaves
// the image alone and Lima uses xz, whose threaded decode is faster.
//
// Lima reads the image under two names: the vz/qemu path (Prepare) reads
// <inst.Dir>/image, while the WSL2 driver (EnsureFs) reads <inst.Dir>/basedisk.
// rdd writes image and, for WSL2, hardlinks basedisk to it, so Lima finds each
// ready and skips its own download-and-decompress.
func ensureImageDecompressed(ctx context.Context, inst *limatype.Instance) error {
	imagePath := filepath.Join(inst.Dir, filenames.Image)
	diskPath := filepath.Join(inst.Dir, filenames.Disk)
	if _, err := os.Stat(diskPath); err == nil {
		return nil
	}
	if _, err := exec.LookPath("xz"); err == nil {
		return nil
	}

	if _, err := os.Stat(imagePath); err != nil {
		if err := decompressImage(ctx, inst, imagePath); err != nil {
			return err
		}
	}
	return linkWSL2BaseDisk(inst, imagePath)
}

// decompressImage downloads the arch-matching xz image through Lima's verified
// downloader and decompresses it to imagePath. It returns nil without writing
// imagePath when no xz image matches the host arch, leaving Prepare to handle
// that case with its own decompressor.
func decompressImage(ctx context.Context, inst *limatype.Instance, imagePath string) error {
	logger := log.FromContext(ctx)
	arch := *inst.Config.Arch
	for _, img := range inst.Config.Images {
		if img.File.Arch != arch {
			continue
		}
		// rdd's templates point at .xz images. A location without an .xz
		// suffix falls through to Prepare, which runs its own decompressor.
		if !strings.HasSuffix(img.File.Location, ".xz") {
			return nil
		}
		cached, err := fileutils.DownloadFile(ctx, "", img.File, false, "the image", arch)
		if err != nil {
			return fmt.Errorf("caching image for in-process decompression: %w", err)
		}
		logger.Info("Decompressing image with in-process xz decoder (no system xz found)",
			"location", img.File.Location)
		// rdd's templates list one .xz image per arch; this skips Prepare's
		// mirror fallback and kernel/initrd sidecars, which no current template
		// needs.
		return xz.DecompressFile(ctx, cached, imagePath)
	}
	// No image matched this arch; let Prepare surface its usual error.
	return nil
}

// linkWSL2BaseDisk hardlinks <inst.Dir>/basedisk to imagePath for WSL2
// instances, so the WSL2 driver's EnsureFs reuses the decompressed image
// instead of shelling out to xz. It is a no-op for other VM types, when the
// image was left for Prepare, or when basedisk already exists.
func linkWSL2BaseDisk(inst *limatype.Instance, imagePath string) error {
	if inst.VMType != limatype.WSL2 {
		return nil
	}
	if _, err := os.Stat(imagePath); err != nil {
		return nil
	}
	baseDiskPath := filepath.Join(inst.Dir, filenames.BaseDiskLegacy)
	if _, err := os.Stat(baseDiskPath); err == nil {
		return nil
	}
	if err := os.Link(imagePath, baseDiskPath); err != nil {
		return fmt.Errorf("linking basedisk for WSL2: %w", err)
	}
	return nil
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
// The same deferral covers a change arriving mid-restart: its template
// differs from the on-disk copy, so the method rewrites the copy and keeps
// deferring the version update. observedTemplateResourceVersion catches up
// only once the running instance matches the latest ConfigMap, so a burst
// of changes settles on the last one.
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

// setCondition updates or adds a condition in the LimaVM status. It logs
// every state change so reconciler traces expose the full condition
// timeline without reading K8s events.
func (r *LimaVMReconciler) setCondition(ctx context.Context, limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) {
	changed := apimeta.SetStatusCondition(&limaVM.Status.Conditions, metav1.Condition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: base.TruncateConditionMessage(message),
	})
	if !changed {
		return
	}
	log.FromContext(ctx).Info("Condition changed",
		"type", conditionType, "status", status, "reason", reason, "message", message)
	if r.Recorder != nil {
		r.Recorder.Eventf(limaVM, nil, corev1.EventTypeNormal, "ConditionChanged", conditionType, message)
	}
}

// updateCondition sets a condition and patches the status subresource.
func (r *LimaVMReconciler) updateCondition(ctx context.Context, limaVM *v1alpha1.LimaVM, conditionType string, status metav1.ConditionStatus, reason, message string) error {
	// Patch (not Update) avoids resource version conflicts when the object is
	// modified externally during long-running operations like waitForPIDFile or
	// graceful shutdown.
	patch := client.MergeFrom(limaVM.DeepCopy())
	r.setCondition(ctx, limaVM, conditionType, status, reason, message)
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
// It signals each hostagent, waits for it to exit (or time out), then reclaims any
// resources left behind — always, because a force-killed hostagent skips its own
// teardown (the usual case on Windows).
func (r *LimaVMReconciler) shutdownAllHostagents() {
	r.instanceStatesMu.RLock()
	states := maps.Clone(r.instanceStates)
	r.instanceStatesMu.RUnlock()

	if len(states) == 0 {
		return
	}

	// Send a graceful shutdown signal to all hostagents in parallel. Skip any whose
	// process has already been reaped; once cmd.Wait() closes procExited it has
	// released the OS handle and the PID may be reused, so there is nothing to
	// signal.
	for name, state := range states {
		if state.cmd == nil || state.cmd.Process == nil {
			continue
		}
		select {
		case <-state.procExited:
			continue
		default:
		}
		_ = process.Interrupt(process.HostagentInterruptKey(name), state.cmd.Process.Pid)
	}

	// Wait for each hostagent process to exit. The watcher goroutine may finish
	// before the process (events.Watch returns on context cancel), so we wait on
	// procExited (closed by haCmd.Wait) rather than done (closed by the watcher).
	// TODO: Wait on all hostagents in parallel instead of sequentially; with the
	// current loop, the total wait is N × gracefulShutdownTimeout in the worst case.
	for name, state := range states {
		// Give the hostagent a chance to exit on its own after the signal above.
		select {
		case <-state.procExited:
		case <-time.After(gracefulShutdownTimeout):
		}

		// Always reclaim the instance's resources, even when procExited closed
		// promptly. On Windows the hostagent is force-killed by exec.CommandContext
		// when the manager context is cancelled: procExited then closes right away,
		// so the exit *looks* graceful, but the hostagent never ran its own teardown
		// and left its PID file and WSL2 distro behind — which the next `rdd svc
		// start` mistakes for an orphan. Use a fresh context because the manager
		// context is already cancelled. stopInstanceForcibly removes the PID/socket/tmp
		// files and terminates the distro; it is a no-op when the hostagent already
		// cleaned up after itself. The loop runs on every platform; off Windows the
		// hostagent self-terminates and distro termination is a no-op, so the
		// reclaim finds nothing to do.
		logger := ctrl.Log.WithName("shutdownAllHostagents")
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if inst, err := store.Inspect(cleanupCtx, name); err == nil && inst != nil {
			// A force-killed hostagent leaves its PID file behind, and on Windows
			// that PID can already be recycled by the time we taskkill;
			// stopInstanceForcibly screens it. On the timeout branch the hostagent
			// is still running and still ours, so the PID is kept and terminated.
			stopInstanceForcibly(cleanupCtx, logger, inst)
		}
		cancel()

		// Wait briefly for the process to exit after a forced kill. This returns
		// at once when the watcher has already reaped the hostagent (closing
		// procExited); the timeout only bounds shutdown if reaping stalls. The
		// hostagent's stdio are plain log files, not pipes, so cmd.Wait runs no
		// I/O-copy goroutine that could keep it blocked.
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		select {
		case <-state.procExited:
		case <-waitCtx.Done():
			logger.Info("Timed out waiting for hostagent to exit after forced kill", "instance", name)
		}
		waitCancel()

		state.cancel()
		r.instanceStatesMu.Lock()
		delete(r.instanceStates, name)
		r.instanceStatesMu.Unlock()
	}
}
