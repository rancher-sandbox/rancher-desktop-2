// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	rddv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostinfo"
)

func newHostInfoScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	assert.NilError(t, rddv1alpha1.AddToScheme(s))
	return s
}

func newSingleton() *rddv1alpha1.HostInfo {
	return &rddv1alpha1.HostInfo{ObjectMeta: metav1.ObjectMeta{Name: SingletonName}}
}

func request(name string) ctrl.Request {
	return ctrl.Request{NamespacedName: client.ObjectKey{Name: name}}
}

// Test_Start_NeverFailsTheManager pins the property that keeps a HostInfo
// bootstrap failure local. Start is registered as a manager Runnable, and a
// Runnable that returns an error aborts the manager, stopping every other
// controller while the daemon still reports the control plane as ready. So a
// permanently failing Create must still leave Start returning nil once the
// manager shuts down.
func Test_Start_NeverFailsTheManager(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.CreateOption) error {
				return errors.New("apiserver is unavailable")
			},
		}).
		Build()

	r := &HostInfoReconciler{Client: c}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	assert.NilError(t, r.Start(ctx))
}

func Test_Start_CreatesTheSingleton(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().WithScheme(newHostInfoScheme(t)).Build()
	r := &HostInfoReconciler{Client: c}

	assert.NilError(t, r.Start(context.Background()))

	var hi rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &hi))
}

// Test_Start_ToleratesAnExistingSingleton covers the restart path: the object
// survives a daemon restart, so the bootstrap must treat it as success.
func Test_Start_ToleratesAnExistingSingleton(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithObjects(newSingleton()).
		Build()
	r := &HostInfoReconciler{Client: c}

	assert.NilError(t, r.Start(context.Background()))
}

func Test_Reconcile_PopulatesStatus(t *testing.T) {
	t.Parallel()

	singleton := newSingleton()
	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithObjects(singleton).
		WithStatusSubresource(singleton).
		Build()
	r := &HostInfoReconciler{Client: c}

	_, err := r.Reconcile(context.Background(), request(SingletonName))
	assert.NilError(t, err)

	var hi rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &hi))
	assert.Assert(t, hi.Status.CPUs >= 1)
	assert.Assert(t, hi.Status.Memory.Value() > 0)
}

// Test_Reconcile_PublishesAZeroReading pins that a failed detection reaches the
// status. A merge patch would diff the zero away against an empty status, so a
// client could not tell a detection failure from an absent field.
func Test_Reconcile_PublishesAZeroReading(t *testing.T) {
	t.Parallel()

	singleton := newSingleton()
	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithObjects(singleton).
		WithStatusSubresource(singleton).
		WithInterceptorFuncs(interceptor.Funcs{
			// The fake client applies a merge patch to the typed object, so it
			// cannot show that a real apiserver receives only the diff. Reject
			// the call instead, so a return to Status().Patch fails here.
			SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
				return errors.New("status must be written in full; a merge patch diffs a zero reading away")
			},
		}).
		Build()
	r := &HostInfoReconciler{
		Client: c,
		detect: func() hostinfo.HostInfo { return hostinfo.HostInfo{CPUs: 8, Memory: 0} },
	}

	_, err := r.Reconcile(context.Background(), request(SingletonName))
	assert.NilError(t, err)

	var hi rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &hi))
	assert.Equal(t, hi.Status.CPUs, 8)
	assert.Assert(t, hi.Status.Memory.IsZero())

	// The zero must survive serialization, not merely round-trip as a Go value.
	encoded, err := json.Marshal(hi.Status)
	assert.NilError(t, err)
	assert.Equal(t, string(encoded), `{"cpus":8,"memory":"0"}`)
}

// Test_Reconcile_IgnoresForeignNames pins that only the singleton carries host
// limits. Populating a user-created object would lend it the same authority.
func Test_Reconcile_IgnoresForeignNames(t *testing.T) {
	t.Parallel()

	foreign := &rddv1alpha1.HostInfo{ObjectMeta: metav1.ObjectMeta{Name: "rogue"}}
	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithObjects(foreign).
		WithStatusSubresource(foreign).
		Build()
	r := &HostInfoReconciler{Client: c}

	_, err := r.Reconcile(context.Background(), request("rogue"))
	assert.NilError(t, err)

	var hi rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: "rogue"}, &hi))
	assert.Equal(t, hi.Status.CPUs, 0)
	assert.Assert(t, hi.Status.Memory.IsZero())
}

// Test_Reconcile_IsANoOpOnceConverged pins that a settled Status is not
// rewritten. The status patch schedules another reconcile, so patching
// unconditionally would log and write on every convergence pass.
func Test_Reconcile_IsANoOpOnceConverged(t *testing.T) {
	t.Parallel()

	singleton := newSingleton()
	c := fake.NewClientBuilder().
		WithScheme(newHostInfoScheme(t)).
		WithObjects(singleton).
		WithStatusSubresource(singleton).
		Build()
	r := &HostInfoReconciler{Client: c}

	_, err := r.Reconcile(context.Background(), request(SingletonName))
	assert.NilError(t, err)

	var populated rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &populated))

	_, err = r.Reconcile(context.Background(), request(SingletonName))
	assert.NilError(t, err)

	var settled rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &settled))
	assert.Equal(t, settled.ResourceVersion, populated.ResourceVersion)
}

// Test_Reconcile_RecreatesADeletedSingleton covers the runtime-delete path: no
// watch event fires for an object that never existed, so this branch handles a
// deletion only, and Start's retry loop covers a failed bootstrap.
func Test_Reconcile_RecreatesADeletedSingleton(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().WithScheme(newHostInfoScheme(t)).Build()
	r := &HostInfoReconciler{Client: c}

	_, err := r.Reconcile(context.Background(), request(SingletonName))
	assert.NilError(t, err)

	var hi rddv1alpha1.HostInfo
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: SingletonName}, &hi))
}

// Test_Reconcile_IgnoresAMissingForeignName pins that a deleted rogue object is
// not resurrected under its own name.
func Test_Reconcile_IgnoresAMissingForeignName(t *testing.T) {
	t.Parallel()

	c := fake.NewClientBuilder().WithScheme(newHostInfoScheme(t)).Build()
	r := &HostInfoReconciler{Client: c}

	_, err := r.Reconcile(context.Background(), request("rogue"))
	assert.NilError(t, err)

	err = c.Get(context.Background(), client.ObjectKey{Name: "rogue"}, &rddv1alpha1.HostInfo{})
	assert.Assert(t, apierrors.IsNotFound(err))
}
