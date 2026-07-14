// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the HostInfo reconciler, which maintains a
// cluster-scoped singleton that exposes host hardware limits in its Status.
package controllers

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	rddv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostinfo"
)

// SingletonName is the fixed name of the HostInfo singleton.
const SingletonName = "system"

// HostInfoReconciler reconciles the HostInfo singleton.
type HostInfoReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=hostinfos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rdd.rancherdesktop.io,resources=hostinfos/status,verbs=get;update;patch

// Reconcile reads the host hardware limits and writes them to HostInfo.Status.
func (r *HostInfoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var hi rddv1alpha1.HostInfo
	if err := r.Get(ctx, req.NamespacedName, &hi); err != nil {
		if apierrors.IsNotFound(err) && req.Name == SingletonName {
			// The singleton was deleted at runtime. Recreate it so its Status
			// is repopulated; the create schedules another reconcile that lands
			// in the Status.Patch path below. (An initial bootstrap failure
			// cannot reach here, since no watch event fires for an object that
			// never existed; Start's retry loop covers that case instead.)
			log.Info("HostInfo singleton missing; recreating it")
			return ctrl.Result{}, r.bootstrapSingleton(ctx)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	info := hostinfo.Detect()

	patch := client.MergeFrom(hi.DeepCopy())
	hi.Status.CPUs = info.CPUs
	hi.Status.Memory = info.Memory

	if err := r.Status().Patch(ctx, &hi, patch); err != nil {
		log.Error(err, "Failed to update HostInfo status")
		return ctrl.Result{}, err
	}
	log.Info("Updated HostInfo status", "cpus", info.CPUs, "memory", info.Memory)
	return ctrl.Result{}, nil
}

// bootstrapRetryInterval is how long Start waits between failed attempts to
// create the HostInfo singleton.
const bootstrapRetryInterval = 5 * time.Second

// Start implements manager.Runnable. It bootstraps the HostInfo singleton once
// the cache is ready so that the reconciler loop can populate its Status. Only
// the leader runs this to avoid concurrent creates.
//
// It must never return a non-nil error outside of shutdown: Start is registered
// as a manager Runnable, and a Runnable that returns an error aborts the whole
// manager, stopping every controller while the daemon still reports the control
// plane as ready. So a bootstrap failure is retried until it succeeds or the
// manager is shutting down; a runtime delete is instead recovered by Reconcile.
func (r *HostInfoReconciler) Start(ctx context.Context) error {
	log := logf.FromContext(ctx)
	// PollUntilContextCancel runs the condition immediately, then every
	// interval, until it returns true or ctx is cancelled. The condition never
	// returns an error, so the only way this exits non-nil is ctx cancellation
	// (normal shutdown), which we deliberately swallow so the manager is never
	// aborted by this Runnable.
	_ = wait.PollUntilContextCancel(ctx, bootstrapRetryInterval, true, func(ctx context.Context) (bool, error) {
		if err := r.bootstrapSingleton(ctx); err != nil {
			log.Error(err, "Failed to bootstrap HostInfo singleton; will retry")
			return false, nil
		}
		return true, nil
	})
	return nil
}

// bootstrapSingleton creates the HostInfo singleton, treating an existing object
// as success so it is safe to call from both Start and Reconcile.
func (r *HostInfoReconciler) bootstrapSingleton(ctx context.Context) error {
	hi := &rddv1alpha1.HostInfo{
		ObjectMeta: metav1.ObjectMeta{Name: SingletonName},
	}
	if err := r.Create(ctx, hi); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to bootstrap HostInfo singleton: %w", err)
	}
	return nil
}

// NeedLeaderElection implements manager.LeaderElectionRunnable so that only
// the leader creates the HostInfo singleton.
func (r *HostInfoReconciler) NeedLeaderElection() bool { return true }

// SetupWithManager sets up the controller with the Manager.
func (r *HostInfoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.Add(r); err != nil {
		return err
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&rddv1alpha1.HostInfo{}).
		Complete(r)
}
