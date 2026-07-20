// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the App reconciler, which propagates the
// desired running state to the owned LimaVM and mirrors its conditions back to
// App status.
package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/predicates"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// ControllerDiscovery enumerates controllers enabled across all controller
// managers in this control plane. AppReconciler queries it during
// reconciliation to decide whether Settled must wait for
// ContainerEngineReady or KubernetesReady. The interface mirrors a small
// subset of pkg/service/controllers.ControllerManagerDiscovery so tests can
// substitute a fake without taking on the full dependency.
type ControllerDiscovery interface {
	GetEnabledControllers(ctx context.Context) ([]string, error)
}

const (
	appName                        = "app"
	limaVMName, inputConfigMapName = "rd", "rd"

	// requeueAfterDeletion is how long to wait between checks while the LimaVM
	// controller is running its teardown (stopping the VM, removing disk files).
	requeueAfterDeletion = 2 * time.Second
)

// Messages for the Settled condition. Kept as constants so tests can
// assert on them without duplicating string literals.
const (
	settledMessageSettled              = "App has reached the desired state"
	settledMessageWaitingForLimaVM     = "Waiting for LimaVM to report its state"
	settledMessageWaitingForEngine     = "Waiting for container engine condition"
	settledMessageEngineStale          = "Container engine needs to be synchronized"
	settledMessageWaitingForKubernetes = "Waiting for Kubernetes condition"
	settledMessageKubernetesStale      = "Kubernetes context needs to be synchronized"
	settledMessageLimaVMNotReached     = "LimaVM has not yet reached "
	settledMessageApplyingTemplate     = "Applying the configuration change to the VM"
)

// AppReconciler reconciles the singleton App resource and manages its LimaVM lifecycle.
type AppReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Manager          ctrl.Manager
	LimaTemplateData string

	// Discovery is consulted on each reconcile to determine whether the
	// engine and/or kubernetes controllers are enabled in any controller
	// manager. When enabled, Settled gates on ContainerEngineReady /
	// KubernetesReady respectively. nil disables both gates, which is
	// appropriate for unit tests that do not exercise those controllers.
	Discovery ControllerDiscovery
}

// engineEnabled reports whether ContainerEngineReady should gate Settled.
// On discovery errors it defaults to true so the wait does not return
// prematurely while discovery is transiently unavailable.
func (r *AppReconciler) engineEnabled(ctx context.Context) bool {
	if r.Discovery == nil {
		return false
	}
	enabled, err := r.Discovery.GetEnabledControllers(ctx)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to query controller-manager discovery; assuming engine is enabled")
		return true
	}
	return slices.Contains(enabled, v1alpha1.EngineControllerName)
}

// kubernetesEnabled reports whether KubernetesReady should gate Settled
// (only when spec.kubernetes.enabled is also true).
// On discovery errors it defaults to true so the wait does not return
// prematurely while discovery is transiently unavailable.
func (r *AppReconciler) kubernetesEnabled(ctx context.Context) bool {
	if r.Discovery == nil {
		return false
	}
	enabled, err := r.Discovery.GetEnabledControllers(ctx)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to query controller-manager discovery; assuming kubernetes controller is enabled")
		return true
	}
	return slices.Contains(enabled, v1alpha1.KubernetesControllerName)
}

func applySpecToTemplate(baseTemplate string, spec v1alpha1.AppSpec, kubernetesPort int) (string, error) {
	hostHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get host home directory: %w", err)
	}
	// vm-switch writes its logfile under LogDir() (passed below as VM_SWITCH_LOG)
	// and fails to start if the directory is missing, so create it before the VM.
	if err := os.MkdirAll(instance.LogDir(), 0o700); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	// cpus and memory are appended here rather than baked into the template so
	// the spec drives them directly. Lima falls back to its own default when a
	// key is absent, which covers the unset case (cpus 0, memory nil).
	lines := []string{baseTemplate}
	if cpus := spec.VirtualMachine.CPUs; cpus > 0 {
		lines = append(lines, fmt.Sprintf("cpus: %d", cpus))
	}
	if mem := spec.VirtualMachine.Memory; mem != nil {
		lines = append(lines, fmt.Sprintf("memory: %q", strconv.FormatInt(mem.Value(), 10)))
	}

	lines = append(lines,
		"",
		"param:",
		fmt.Sprintf("  CONTAINER_ENGINE: %s", spec.ContainerEngine.Name),
		fmt.Sprintf("  HOST_DOCKER_SOCKET: %q", instance.DockerSocket()),
		fmt.Sprintf("  HOST_HOME_GUEST: %q", toLinuxPath(hostHome)),
		fmt.Sprintf("  HOST_INSTANCE_CONFIG: %q", toLinuxPath(instance.K3sConfig())),
		fmt.Sprintf("  VM_SWITCH_LOG: %q", toLinuxPath(filepath.Join(instance.LogDir(), "vm-switch.log"))),
		fmt.Sprintf("  NETWORK_SETUP_EXTRA_ARGS: %q", networkSetupExtraArgs()),
		fmt.Sprintf("  GUEST_LOG_DIR: %q", guestLogDir()),
		fmt.Sprintf("  KUBERNETES_ENABLED: %v", spec.Kubernetes.Enabled),
		fmt.Sprintf("  KUBERNETES_VERSION: %s", spec.Kubernetes.Version),
		fmt.Sprintf("  KUBERNETES_PORT: %d", kubernetesPort),
		"",
	)
	return strings.Join(lines, "\n"), nil
}

// networkSetupExtraArgs returns the diagnostic vm-switch logging flags under
// RDD_KEEP_LOGS, empty otherwise. --trace-packets is gated separately behind
// RDD_TRACE_PACKETS because it captures every packet sent or received, decodes
// it, and writes it to the log. This significantly affects throughput and
// timing, which in turn can change the failures it is meant to capture.
func networkSetupExtraArgs() string {
	if os.Getenv("RDD_KEEP_LOGS") == "" {
		return ""
	}
	args := "--vm-switch-logfile-append"
	if os.Getenv("RDD_TRACE_PACKETS") != "" {
		args += " --trace-packets"
	}
	return args
}

// guestLogDir returns the guest-visible host log dir under RDD_KEEP_LOGS, empty
// otherwise. The lima template's capture script streams guest journals and
// lima-init.log there; empty makes it a no-op.
func guestLogDir() string {
	if os.Getenv("RDD_KEEP_LOGS") == "" {
		return ""
	}
	return toLinuxPath(instance.LogDir())
}

// toLinuxPath converts a host path to a Linux-accessible path inside a Lima VM.
// On Windows, os.UserHomeDir() returns a Windows path (e.g. C:\Users\foo).
// Inside a WSL2 Lima VM the Windows filesystem is mounted at /mnt/<drive>/...,
// so we convert it to WSL2 supported path. On other platforms the path is returned unchanged.
func toLinuxPath(hostPath string) string {
	if goruntime.GOOS != "windows" {
		return hostPath
	}
	if len(hostPath) >= 2 && hostPath[1] == ':' {
		drive := strings.ToLower(string(hostPath[0]))
		rest := strings.ReplaceAll(hostPath[2:], `\`, `/`)
		// /mnt/ is the mount point for drvfs disks in WSL2, per the default
		// value of `[automount] root=` in `/etc/wsl.conf`.
		return "/mnt/" + drive + rest
	}
	return hostPath
}

// Reconcile implements a singleton app reconciliation loop.
// The app controller is a cluster-scoped singleton - only one instance named 'app' is allowed.
func (r *AppReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app v1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		if !apierrors.IsNotFound(err) {
			log.Error(err, "Unable to fetch App singleton")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.V(1).Info("reconcile entered",
		"specRunning", app.Spec.Running,
		"generation", app.Generation,
		"resourceVersion", app.ResourceVersion,
		"beingDeleted", app.DeletionTimestamp != nil,
	)

	// Handle deletion, delete owned resources.
	if base.IsBeingDeleted(&app) {
		log.Info("App resource is being deleted, performing cleanup")

		namespace := app.GetResourceNamespace()

		// Delete the LimaVM and wait for it to finish cleaning up.
		limaVM := &limav1alpha1.LimaVM{}
		err := r.Get(ctx, client.ObjectKey{Name: limaVMName, Namespace: namespace}, limaVM)
		if err == nil {
			if err := base.RemoveOwnedFinalizer(ctx, r.Client, limaVM, v1alpha1.AppKind); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove owned finalizer from LimaVM: %w", err)
			}
			if !base.IsBeingDeleted(limaVM) {
				if err := r.Delete(ctx, limaVM); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("failed to delete LimaVM: %w", err)
				}
				log.Info("Requested LimaVM deletion, waiting for teardown")
			} else {
				log.Info("Waiting for LimaVM deletion to complete")
			}
			return ctrl.Result{RequeueAfter: requeueAfterDeletion}, nil
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to fetch LimaVM: %w", err)
		}

		// LimaVM is gone. Clean up any ConfigMaps that may have been left behind.
		inputCM := &corev1.ConfigMap{}
		if cmErr := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM); cmErr == nil {
			if cmErr := r.Delete(ctx, inputCM); cmErr != nil && !apierrors.IsNotFound(cmErr) {
				return ctrl.Result{}, fmt.Errorf("failed to delete input ConfigMap during cleanup: %w", cmErr)
			}
		} else if !apierrors.IsNotFound(cmErr) {
			return ctrl.Result{}, fmt.Errorf("failed to fetch input ConfigMap during cleanup: %w", cmErr)
		}

		// Remove any other resources owned by the App.
		if err := base.DeleteOwnedResources(ctx, r.Client, &app, r.Manager); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete owned resources during cleanup: %w", err)
		}

		// Remove the namespace if it was created by this controller.
		ns := &corev1.Namespace{}
		if nsErr := r.Get(ctx, client.ObjectKey{Name: namespace}, ns); nsErr == nil {
			if metav1.IsControlledBy(ns, &app) {
				if nsErr := r.Delete(ctx, ns); nsErr != nil && !apierrors.IsNotFound(nsErr) {
					return ctrl.Result{}, fmt.Errorf("failed to delete namespace during cleanup: %w", nsErr)
				}
			}
		} else if !apierrors.IsNotFound(nsErr) {
			return ctrl.Result{}, fmt.Errorf("failed to fetch namespace during cleanup: %w", nsErr)
		}

		// Everything has been deleted, remove the App finalizer to allow the App resource to be removed.
		return ctrl.Result{}, base.RemoveCleanupFinalizer(ctx, r.Client, &app)
	}

	// Make sure the App is finalized so deletion goes through cleanup.
	if added, err := base.EnsureCleanupFinalizer(ctx, r.Client, &app); err != nil {
		return ctrl.Result{}, err
	} else if added {
		return ctrl.Result{}, nil
	}

	namespace := app.GetResourceNamespace()

	// Create the namespace if it does not exist.
	ns := &corev1.Namespace{}
	err := r.Get(ctx, client.ObjectKey{Name: namespace}, ns)
	if apierrors.IsNotFound(err) {
		ns = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		if err := ctrl.SetControllerReference(&app, ns, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set owner reference on namespace: %w", err)
		}
		if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
			return ctrl.Result{}, fmt.Errorf("failed to create namespace: %w", err)
		}
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch namespace: %w", err)
	}

	// Check whether the LimaVM already exists. If not, create the input ConfigMap and LimaVM.
	limaVM := &limav1alpha1.LimaVM{}
	limaVMErr := r.Get(ctx, client.ObjectKey{Name: limaVMName, Namespace: namespace}, limaVM)
	if limaVMErr != nil && !apierrors.IsNotFound(limaVMErr) {
		return ctrl.Result{}, limaVMErr
	}

	// Resolve the host port for the k3s API and persist it. ResolvePort
	// closes the listener immediately after probing, leaving a TOCTOU
	// window before Lima's identity port-forward rule binds the same port
	// (see AppStatus.KubernetesPort). If the port is stolen during that
	// window, Lima logs a warning and kubectl gets connection refused.
	if app.Spec.Kubernetes.Enabled && app.Status.KubernetesPort == 0 {
		port, err := controllers.ResolvePort(ctx, 7441+instance.Index())
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to resolve Kubernetes port: %w", err)
		}
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &v1alpha1.App{}
			if err := r.Get(ctx, client.ObjectKey{Name: appName}, latest); err != nil {
				return err
			}
			if latest.Status.KubernetesPort != 0 {
				return nil
			}
			latest.Status.KubernetesPort = port
			return r.Status().Update(ctx, latest)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update App status: %w", err)
		}
		// The status update causes a requeue.
		return ctrl.Result{}, nil
	}

	// Clear a stale port when Kubernetes is disabled so the next enable
	// resolves a fresh port rather than reusing one that may have been
	// claimed by another process during the intervening idle window.
	if !app.Spec.Kubernetes.Enabled && app.Status.KubernetesPort != 0 {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &v1alpha1.App{}
			if err := r.Get(ctx, client.ObjectKey{Name: appName}, latest); err != nil {
				return err
			}
			if latest.Status.KubernetesPort == 0 {
				return nil
			}
			latest.Status.KubernetesPort = 0
			return r.Status().Update(ctx, latest)
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clear Kubernetes port: %w", err)
		}
		return ctrl.Result{}, nil
	}

	if apierrors.IsNotFound(limaVMErr) {
		template, err := applySpecToTemplate(r.LimaTemplateData, app.Spec, app.Status.KubernetesPort)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to apply spec to template: %w", err)
		}
		inputCM := &corev1.ConfigMap{}
		cmErr := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputCM)
		if cmErr != nil && !apierrors.IsNotFound(cmErr) {
			return ctrl.Result{}, cmErr
		}
		if apierrors.IsNotFound(cmErr) {
			inputCM = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      inputConfigMapName,
					Namespace: namespace,
				},
				Data: map[string]string{
					limav1alpha1.TemplateConfigMapKey: template,
				},
			}
			if err := ctrl.SetControllerReference(&app, inputCM, r.Scheme); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to set owner reference on input ConfigMap: %w", err)
			}
			if err := r.Create(ctx, inputCM); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create input ConfigMap: %w", err)
			}
		}

		limaVM = &limav1alpha1.LimaVM{
			ObjectMeta: metav1.ObjectMeta{
				Name:       limaVMName,
				Namespace:  namespace,
				Finalizers: []string{base.OwnedFinalizerFor(v1alpha1.AppKind)},
			},
			Spec: limav1alpha1.LimaVMSpec{
				TemplateRef: limav1alpha1.TemplateReference{
					Name:      inputConfigMapName,
					Namespace: namespace,
				},
				Running: app.Spec.Running,
			},
		}
		if err := ctrl.SetControllerReference(&app, limaVM, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set owner reference on LimaVM: %w", err)
		}
		if err := r.Create(ctx, limaVM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create LimaVM: %w", err)
		}
		log.Info("Created LimaVM", "name", limaVMName, "namespace", namespace)
		return ctrl.Result{}, nil
	}

	// LimaVM exists — clean up the input ConfigMap if it still lingers from the
	// creation phase and return to requeueing on LimaVM updates.
	inputConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Name: inputConfigMapName, Namespace: namespace}, inputConfigMap); err == nil {
		if err := r.Delete(ctx, inputConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete input ConfigMap: %w", err)
		}
		log.Info("Deleted input ConfigMap after LimaVM created its own copy")
		// ConfigMaps are not watched (no Owns(&corev1.ConfigMap{})), so deleting
		// one produces no watch event. Requeue explicitly to guarantee the next
		// reconcile runs rather than relying on implicit LimaVM status activity.
		return ctrl.Result{Requeue: true}, nil
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to fetch input ConfigMap: %w", err)
	}

	if limaVM.Spec.Running && !app.Spec.Running {
		limaVM.Spec.Running = false
		if err := r.Update(ctx, limaVM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to propagate running state to LimaVM: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Propagate app spec.containerEngine and spec.kubernetes into the LimaVM's
	// template ConfigMap. templateUpToDate reports whether the LimaVM is running
	// the current template; computeSettledCondition gates on it so `rdd set`
	// cannot return before a template change has actually been applied.
	templateUpToDate := true
	if limaVM.Status.TemplateConfigMap != "" {
		templateCM := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Name: limaVM.Status.TemplateConfigMap, Namespace: namespace}, templateCM); err != nil {
			if !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to fetch LimaVM template ConfigMap: %w", err)
			}
		} else {
			desired, err := applySpecToTemplate(r.LimaTemplateData, app.Spec, app.Status.KubernetesPort)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to apply spec to template: %w", err)
			}
			if templateCM.Data[limav1alpha1.TemplateConfigMapKey] != desired {
				patch := client.MergeFrom(templateCM.DeepCopy())
				if templateCM.Data == nil {
					templateCM.Data = make(map[string]string)
				}
				templateCM.Data[limav1alpha1.TemplateConfigMapKey] = desired
				if err := r.Patch(ctx, templateCM, patch); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to update template ConfigMap: %w", err)
				}
				log.Info("Updated template ConfigMap",
					"containerEngine", app.Spec.ContainerEngine.Name,
					"kubernetesEnabled", app.Spec.Kubernetes.Enabled,
					"kubernetesVersion", app.Spec.Kubernetes.Version)
				// ConfigMaps are not watched, so requeue to let the reconciler
				// evaluate remaining spec fields (e.g. running). Settled stays
				// False until the LimaVM restarts into the new template, because
				// the templateUpToDate check below sees the stale observed
				// version.
				return ctrl.Result{Requeue: true}, nil
			}
			// LimaVM defers ObservedTemplateResourceVersion until the restart
			// completes, so a matching resourceVersion means the running
			// instance reflects the current template.
			templateUpToDate = templateCM.ResourceVersion == limaVM.Status.ObservedTemplateResourceVersion
		}
	}

	if !limaVM.Spec.Running && app.Spec.Running {
		limaVM.Spec.Running = true
		if err := r.Update(ctx, limaVM); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to propagate running state to LimaVM: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// Mirror LimaVM status conditions and compute Settled. The engine
	// reconciler writes ContainerEngineReady on the same object, so
	// app's resourceVersion from the initial Get can be stale.
	// retry.RetryOnConflict + re-Get matches
	// EngineReconciler.setEngineCondition; without it, concurrent
	// writers 409-loop through controller-runtime requeues.
	engineEnabled := r.engineEnabled(ctx)
	kubernetesEnabled := r.kubernetesEnabled(ctx)
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.App{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err != nil {
			return err
		}
		changed := false
		for _, cond := range limaVM.Status.Conditions {
			// Defensive: guards against a future LimaVM bypass that would fail CRD validation.
			msg := base.TruncateConditionMessage(cond.Message)
			changed = apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               cond.Type,
				Status:             cond.Status,
				Reason:             cond.Reason,
				Message:            msg,
				ObservedGeneration: latest.Generation,
				LastTransitionTime: cond.LastTransitionTime,
			}) || changed
		}
		// engineEnabled, kubernetesEnabled, and templateUpToDate are all computed
		// from the outer app read. If a newer generation lands mid-loop they are
		// briefly stale against latest, but the next reconcile re-derives them
		// and corrects Settled.
		settled := computeSettledCondition(latest, settledInputs{
			engineEnabled:     engineEnabled,
			kubernetesEnabled: kubernetesEnabled,
			templateUpToDate:  templateUpToDate,
		})
		changed = apimeta.SetStatusCondition(&latest.Status.Conditions, settled) || changed
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	}); err != nil {
		log.Error(err, "Unable to update App status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// settledInputs holds the per-reconcile flags that gate Settled. The call
// site sets each field by name, so a caller cannot transpose them.
type settledInputs struct {
	// engineEnabled is false when no controller writes ContainerEngineReady;
	// in that case the engine condition is ignored.
	engineEnabled bool
	// kubernetesEnabled is false when no controller writes KubernetesReady;
	// it also gates on spec.kubernetes.enabled so a stopped cluster does not
	// hold Settled pending.
	kubernetesEnabled bool
	// templateUpToDate is false when the LimaVM has not yet restarted into the
	// current template; it holds Settled at False with reason ApplyingTemplate.
	templateUpToDate bool
}

// computeSettledCondition derives Settled from the feeding conditions
// that live on app.Status.Conditions: the LimaVM Running condition
// (mirrored just above), ContainerEngineReady (written by the engine
// controller), and KubernetesReady (written by the kubernetes controller).
// The output condition carries the App's current generation, so waiters can
// filter out snapshots from earlier spec versions.
//
// Settled answers "has the reconcile chain caught up with the current
// spec?". It goes True when the VM has reached the desired running
// state with a terminal reason (Started/Stopped) and all enabled
// sub-controllers have observed and processed the current generation.
// A transient phase (Starting, Stopping, Reconciling, RestartNeeded)
// holds Settled at False even if the VM is momentarily running, so
// `rdd set` does not return before the chain stabilises.
func computeSettledCondition(app *v1alpha1.App, in settledInputs) metav1.Condition {
	runningCond := apimeta.FindStatusCondition(app.Status.Conditions, v1alpha1.AppConditionRunning)
	engineCond := apimeta.FindStatusCondition(app.Status.Conditions, v1alpha1.AppConditionContainerEngineReady)
	kubeCond := apimeta.FindStatusCondition(app.Status.Conditions, v1alpha1.AppConditionKubernetesReady)
	desiredRunning := app.Spec.Running

	settled := metav1.Condition{
		Type:               v1alpha1.AppConditionSettled,
		ObservedGeneration: app.Generation,
	}

	// A template change triggers a VM restart; until the LimaVM is running the
	// current template, the spec change has not taken effect. Hold Settled to
	// False even when the feeding conditions are momentarily all True at the
	// new generation against the not-yet-restarted VM.
	if !in.templateUpToDate {
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonApplyingTemplate
		settled.Message = settledMessageApplyingTemplate
		return settled
	}

	switch {
	case runningCond == nil:
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonWaitingForLimaVM
		settled.Message = settledMessageWaitingForLimaVM
	case desiredRunning && runningCond.Status != metav1.ConditionTrue:
		settled.Status = metav1.ConditionFalse
		settled.Reason = runningCond.Reason
		settled.Message = runningLimaVMMessage(runningCond, "Started")
	// Status=False covers Stopped as well as the transient Starting/Stopping
	// reasons, so we must match the terminal Stopped reason explicitly.
	case !desiredRunning && runningCond.Reason != "Stopped":
		settled.Status = metav1.ConditionFalse
		settled.Reason = runningCond.Reason
		settled.Message = runningLimaVMMessage(runningCond, "Stopped")
	case !in.engineEnabled && !in.kubernetesEnabled:
		settled.Status = metav1.ConditionTrue
		settled.Reason = v1alpha1.AppSettledReasonSettled
		settled.Message = settledMessageSettled
	case !desiredRunning && in.engineEnabled:
		// Wait for the engine reconciler to confirm cleanup for this
		// generation before declaring settled. Two conditions must both
		// hold:
		//
		//  1. ObservedGeneration >= app.Generation: the engine reconciler
		//     has seen this spec change (prevents a Settled=True from a
		//     prior generation's condition sneaking through).
		//
		//  2. The reason is a terminal stopped state ("Stopped" or
		//     "NotApplicable"): the engine reconciler runs
		//     cleanupMirrorResources and stamps ContainerEngineReady only
		//     after cleanup succeeds.  Without this check a
		//     "Connected/M+1" condition — written while the VM was still
		//     running on an earlier reconcile that saw the spec change but
		//     not yet the stopped VM — would incorrectly satisfy the wait,
		//     causing `rdd set running=false` to return before mirror
		//     resources (Containers, Images, Volumes) are deleted.
		engineSettled := engineCond != nil &&
			engineCond.ObservedGeneration >= app.Generation &&
			(engineCond.Reason == v1alpha1.EngineReasonStopped || engineCond.Reason == v1alpha1.EngineReasonNotApplicable)
		if !engineSettled {
			settled.Status = metav1.ConditionFalse
			settled.Reason = v1alpha1.AppSettledReasonEngineStale
			settled.Message = settledMessageEngineStale
		} else {
			settled.Status = metav1.ConditionTrue
			settled.Reason = v1alpha1.AppSettledReasonSettled
			settled.Message = settledMessageSettled
		}
	case !desiredRunning:
		// Engine is disabled; kubernetes context cleanup is async and
		// does not block settling. A stopped VM is settled regardless.
		settled.Status = metav1.ConditionTrue
		settled.Reason = v1alpha1.AppSettledReasonSettled
		settled.Message = settledMessageSettled
	case in.engineEnabled && engineCond == nil:
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonWaitingForEngine
		settled.Message = settledMessageWaitingForEngine
	case in.engineEnabled && engineCond.ObservedGeneration < app.Generation:
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonEngineStale
		settled.Message = settledMessageEngineStale
	case in.engineEnabled && engineCond.Status != metav1.ConditionTrue:
		settled.Status = metav1.ConditionFalse
		settled.Reason = engineCond.Reason
		settled.Message = engineCond.Message
	case in.kubernetesEnabled && app.Spec.Kubernetes.Enabled && kubeCond == nil:
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonWaitingForKubernetes
		settled.Message = settledMessageWaitingForKubernetes
	case in.kubernetesEnabled && app.Spec.Kubernetes.Enabled && kubeCond.ObservedGeneration < app.Generation:
		settled.Status = metav1.ConditionFalse
		settled.Reason = v1alpha1.AppSettledReasonKubernetesStale
		settled.Message = settledMessageKubernetesStale
	case in.kubernetesEnabled && app.Spec.Kubernetes.Enabled && kubeCond.Status != metav1.ConditionTrue:
		settled.Status = metav1.ConditionFalse
		settled.Reason = kubeCond.Reason
		settled.Message = kubeCond.Message
	default:
		settled.Status = metav1.ConditionTrue
		settled.Reason = v1alpha1.AppSettledReasonSettled
		settled.Message = settledMessageSettled
	}
	return settled
}

// runningLimaVMMessage builds the Settled message when LimaVM's
// Running reason does not match the desired state. Failure reasons
// (ending in "Failed") propagate LimaVM's diagnostic message; other
// reasons get a concise "has not yet reached <desired>" text.
func runningLimaVMMessage(runningCond *metav1.Condition, desired string) string {
	if strings.HasSuffix(runningCond.Reason, "Failed") && runningCond.Message != "" {
		return runningCond.Message
	}
	return settledMessageLimaVMNotReached + desired
}

// SetupWithManager sets up the controller with the Manager.
func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.App{}, builder.WithPredicates(predicates.WatchEventLogger("app"))).
		Owns(&limav1alpha1.LimaVM{}).
		Complete(r)
}
