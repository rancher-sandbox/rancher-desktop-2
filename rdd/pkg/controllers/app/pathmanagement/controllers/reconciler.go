// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the PATH management reconciler, which keeps the
// user's PATH in sync with spec.application.addPath.
package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/predicates"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/managedlines"
)

// appName is the name of the singleton App resource.
const appName = "app"

// pathManagementFinalizer blocks App deletion until we've unwound our PATH
// edits, so `rdd ctl delete app` doesn't orphan a block in the user's startup
// files. It's distinct from the App controller's cleanup finalizer so each
// controller manages its own.
const pathManagementFinalizer = "rdd.rancherdesktop.io/path-management"

// pathManagementLockFile is the name of the cross-process lock that serializes
// PATH edits. It lives in the per-user data root shared by every instance (not
// under a single instance's directory), so two daemons editing the same startup
// files or user Environment take turns. See acquirePathLock.
const pathManagementLockFile = "rancher-desktop-pathmanagement.lock"

// pathManagementLockTimeout bounds how long we wait for the lock before giving
// up, so a stuck holder requeues the reconcile instead of blocking forever.
const pathManagementLockTimeout = 30 * time.Second

// pathUnwindRetryGrace bounds how long App deletion retries a repairable unwind
// failure before giving up and releasing the finalizer anyway. Measured from the
// App's deletionTimestamp, so a momentary problem (a brief full disk) still gets
// a few attempts to clear, but a permanently unreadable startup file — or, on
// Windows, any Environment write failure, since nothing there is ever classified
// permanent — can't strand the App in Terminating forever.
const pathUnwindRetryGrace = 30 * time.Second

// pathUnwindRetryInterval is how long to wait between those bounded retries.
const pathUnwindRetryInterval = 5 * time.Second

// PathManagementReconciler adds or removes the Rancher Desktop bin directory
// from the user's PATH based on spec.application.addPath. It watches the App
// singleton and runs whether or not the VM is up, since PATH edits are host-side.
type PathManagementReconciler struct {
	client.Client

	// BinDir is the directory to add to PATH (e.g. ~/.rd2/bin).
	BinDir string
	// Suffix is the instance suffix (e.g. "2"). It goes into the fence markers so
	// separate instances (and Rancher Desktop 1) don't touch each other's blocks.
	Suffix string
}

// Reconcile applies spec.application.addPath: front/back add the bin directory
// (prepended or appended); manual (or unset) removes any block we added before.
// It is idempotent, so extra reconciles are harmless.
func (r *PathManagementReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app appv1alpha1.App
	if err := r.Get(ctx, client.ObjectKey{Name: appName}, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// The App is being deleted: unwind our PATH edits, then release our
	// finalizer so deletion can finish. Without this, a reconcile during the
	// terminating window would re-apply the block and orphan it once the App is
	// gone. `rdd svc delete` unwinds separately in service.Delete, since it stops
	// the daemon before deleting and leaves no reconciler to run this branch.
	if !app.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&app, pathManagementFinalizer) {
			return ctrl.Result{}, nil
		}
		// An unwind failure is handled best-effort so a bad file can't strand the
		// App in Terminating forever. A malformed block — a dotfile the user
		// hand-edited into an unparseable state — is permanent, so release the
		// finalizer at once. A repairable failure (I/O, read-only home, full disk,
		// or on Windows any Environment write failure) might clear, so retry, but
		// only within a grace window measured from deletionTimestamp: an unrelated
		// file that's permanently unreadable, or a policy-locked Windows
		// Environment, would otherwise requeue forever (controller-runtime backs
		// off but never gives up). After the window we log and release anyway.
		if err := r.apply(ctx, appv1alpha1.AddPathManual, false); err != nil {
			switch {
			case permanentPathError(err):
				log.Error(err, "Managed block is malformed; releasing finalizer without removing it")
			case time.Since(app.DeletionTimestamp.Time) < pathUnwindRetryGrace:
				log.Error(err, "Failed to unwind PATH management before App deletion; will retry")
				return ctrl.Result{RequeueAfter: pathUnwindRetryInterval}, nil
			default:
				log.Error(err, "Failed to unwind PATH management before App deletion within grace period; releasing finalizer anyway")
			}
		}
		controllerutil.RemoveFinalizer(&app, pathManagementFinalizer)
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Ensure our finalizer is present so a later deletion runs the unwind above.
	if controllerutil.AddFinalizer(&app, pathManagementFinalizer) {
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
	}

	strategy := app.Spec.Application.AddPath
	if strategy == "" {
		strategy = appv1alpha1.AddPathManual
	}

	if err := r.apply(ctx, strategy, false); err != nil {
		log.Error(err, "Failed to apply PATH management", "strategy", strategy)
		// Record the failure so Settled (and `rdd set --wait`) can see it instead
		// of reporting success, then requeue on the original error. A malformed
		// block is permanent (only the user can fix it), so it gets a distinct
		// reason: under manual that keeps Settled from wedging on a file we can't
		// repair, while a transient failure still gates.
		reason := appv1alpha1.PathManagementReasonFailed
		if permanentPathError(err) {
			reason = appv1alpha1.PathManagementReasonMalformed
		}
		if condErr := r.setCondition(ctx, app.Name, app.Generation, metav1.ConditionFalse, reason, err.Error()); condErr != nil {
			log.Error(condErr, "Failed to record PathManagementReady failure")
		}
		return ctrl.Result{}, err
	}
	log.V(1).Info("Applied PATH management", "strategy", strategy, "binDir", r.BinDir)
	if err := r.setCondition(ctx, app.Name, app.Generation, metav1.ConditionTrue, appv1alpha1.PathManagementReasonApplied, "PATH management applied"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setCondition writes the PathManagementReady condition on the App. It stamps
// generation (the generation of the App read that chose the strategy) into
// ObservedGeneration, not the re-Get's generation: if the spec changed between
// the two reads, this result belongs to the earlier generation, and unlike the
// engine controller there's no second gate holding Settled, so stamping the
// newer number would let `rdd set --wait` settle before the new strategy is
// applied. The next reconcile (triggered by the spec change) stamps the newer
// generation. It mirrors the engine controller otherwise: the App controller
// writes the same object in parallel, so a naive Update races and 409s; retry on
// conflict with a re-Get.
func (r *PathManagementReconciler) setCondition(ctx context.Context, name string, generation int64, status metav1.ConditionStatus, reason, message string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &appv1alpha1.App{}
		if err := r.Get(ctx, client.ObjectKey{Name: name}, latest); err != nil {
			// Concurrent `rdd svc delete` can remove the App mid-reconcile.
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		changed := meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               appv1alpha1.AppConditionPathManagementReady,
			Status:             status,
			Reason:             reason,
			Message:            base.TruncateConditionMessage(message),
			ObservedGeneration: generation,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	})
}

// Unwind removes this instance's bin directory from the user's PATH, leaving it
// unmanaged. Service deletion calls it before removing the bin directory, so the
// user isn't left with an entry pointing at a directory that no longer exists.
// It force-removes the entry even from the middle of the Windows Path: the whole
// instance is going away, so a placement we'd otherwise preserve under manual
// would just dangle.
func (r *PathManagementReconciler) Unwind(ctx context.Context) error {
	return r.apply(ctx, appv1alpha1.AddPathManual, true)
}

// apply serializes the platform edit (shell startup files on Unix, the user
// Environment on Windows) behind a cross-process lock, then runs it. The lock is
// what makes the fence markers' promise — that separate instances can share one
// ~/.zshrc — hold across daemons: without it, two instances read, compute, and
// write concurrently, and the last writer drops the other's block. The in-process
// mutex in atomicfile can't help, since the instances are separate processes and
// the read/compute happens before the write anyway.
func (r *PathManagementReconciler) apply(ctx context.Context, strategy appv1alpha1.AddPathStrategy, forceRemove bool) error {
	unlock, err := acquirePathLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return r.applyPath(ctx, strategy, forceRemove)
}

// permanentPathError reports whether err is non-nil and every underlying cause is
// a malformed managed block — a file the user corrupted that retrying can't fix.
// applyPosix joins one error per startup file, so a single transient cause (I/O,
// permissions, a full disk) anywhere makes the whole failure repairable: the
// reconciler keeps retrying and Settled keeps gating until it clears. Only when
// every cause is permanent do we stop retrying and drop the manual Settled gate.
func permanentPathError(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !permanentPathError(cause) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, managedlines.ErrMalformedBlock)
}

// acquirePathLock takes the shared PATH lock and returns a function that
// releases it. It blocks up to pathManagementLockTimeout; a stuck holder surfaces
// as an error so the reconcile requeues rather than hanging.
func acquirePathLock(ctx context.Context) (func(), error) {
	dir, err := instance.SharedDataDir()
	if err != nil {
		return nil, fmt.Errorf("locate PATH lock directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create PATH lock directory: %w", err)
	}
	fl := flock.New(filepath.Join(dir, pathManagementLockFile))

	lockCtx, cancel := context.WithTimeout(ctx, pathManagementLockTimeout)
	defer cancel()
	locked, err := fl.TryLockContext(lockCtx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire PATH lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("acquire PATH lock: timed out after %s", pathManagementLockTimeout)
	}
	return func() { _ = fl.Unlock() }, nil
}

// SetupWithManager wires the reconciler to the App singleton.
func (r *PathManagementReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appv1alpha1.App{}, builder.WithPredicates(predicates.WatchEventLogger("path-management-reconciler"))).
		Named("path-management-reconciler").
		Complete(r)
}
