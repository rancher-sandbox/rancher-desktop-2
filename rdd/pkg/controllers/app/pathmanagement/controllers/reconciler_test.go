// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/managedlines"
)

func reconcilerTestScheme(t *testing.T) *k8sruntime.Scheme {
	t.Helper()
	s := k8sruntime.NewScheme()
	assert.NilError(t, appv1alpha1.AddToScheme(s))
	return s
}

// TestReconcileWritesReadyCondition checks that a successful apply stamps
// PathManagementReady=True with the App's generation.
func TestReconcileWritesReadyCondition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))

	app := &appv1alpha1.App{
		Name: appName, Generation: 3,
		Spec: appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(app).Build()
	r := &PathManagementReconciler{Client: c, BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	var got appv1alpha1.App
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, &got))
	cond := findCondition(got.Status.Conditions, appv1alpha1.AppConditionPathManagementReady)
	assert.Assert(t, cond != nil)
	assert.Equal(t, cond.Status, metav1.ConditionTrue)
	assert.Equal(t, cond.Reason, appv1alpha1.PathManagementReasonApplied)
	assert.Equal(t, cond.ObservedGeneration, int64(3))
}

// TestReconcileWritesFailureCondition checks that an apply failure stamps
// PathManagementReady=False and still requeues on the error. A directory in
// place of ~/.zshrc makes managedlines.Manage fail to read it.
func TestReconcileWritesFailureCondition(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))
	assert.NilError(t, os.Mkdir(filepath.Join(home, ".zshrc"), 0o755))

	app := &appv1alpha1.App{
		Name: appName, Generation: 5,
		Spec: appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(app).Build()
	r := &PathManagementReconciler{Client: c, BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.Assert(t, err != nil)

	var got appv1alpha1.App
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, &got))
	cond := findCondition(got.Status.Conditions, appv1alpha1.AppConditionPathManagementReady)
	assert.Assert(t, cond != nil)
	assert.Equal(t, cond.Status, metav1.ConditionFalse)
	assert.Equal(t, cond.Reason, appv1alpha1.PathManagementReasonFailed)
	assert.Equal(t, cond.ObservedGeneration, int64(5))
}

// TestReconcileStampsAppliedGeneration checks that when the spec changes between
// the read that chose the strategy and the condition write, the condition is
// stamped with the applied generation, not the newer one — otherwise Settled
// could report the new generation as done before its strategy ran.
func TestReconcileStampsAppliedGeneration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))

	app := &appv1alpha1.App{
		Name: appName, Generation: 1,
		Spec: appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)

	// Bump the generation on the setCondition re-Get (the second Get), the way an
	// external spec change landing mid-reconcile would.
	gets := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(app).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := cl.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				gets++
				if a, ok := obj.(*appv1alpha1.App); ok && gets > 1 {
					a.Generation = 2
					a.Spec.Application.AddPath = appv1alpha1.AddPathManual
				}
				return nil
			},
		}).Build()
	r := &PathManagementReconciler{Client: c, BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	var got appv1alpha1.App
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, &got))
	cond := findCondition(got.Status.Conditions, appv1alpha1.AppConditionPathManagementReady)
	assert.Assert(t, cond != nil)
	// We applied front at generation 1; the condition must claim 1, not the
	// generation-2 spec we never applied.
	assert.Equal(t, cond.ObservedGeneration, int64(1))
}

func findCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

// TestReconcileAddsFinalizer checks that a normal reconcile puts our finalizer
// on the App, so a later deletion runs the unwind branch.
func TestReconcileAddsFinalizer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))

	app := &appv1alpha1.App{
		Name: appName, Generation: 1,
		Spec: appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).WithStatusSubresource(app).Build()
	r := &PathManagementReconciler{Client: c, BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	var got appv1alpha1.App
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, &got))
	assert.Assert(t, slices.Contains(got.Finalizers, pathManagementFinalizer))
}

// TestReconcileUnwindsOnDeletion checks that deleting the App removes our block
// and releases the finalizer, rather than orphaning the block.
func TestReconcileUnwindsOnDeletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))
	r := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	zshrc := filepath.Join(home, ".zshrc")
	block := r.startMarker() + "\nexport PATH=\"" + r.BinDir + ":$PATH\"\n" + r.endMarker() + "\n"
	assert.NilError(t, os.WriteFile(zshrc, []byte("keep\n"+block), 0o644))

	now := metav1.NewTime(time.Now())
	app := &appv1alpha1.App{
		Name:              appName,
		Generation:        1,
		DeletionTimestamp: &now,
		Finalizers:        []string{pathManagementFinalizer},
		Spec:              appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r.Client = c

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	// The block is gone (the user's own content survives).
	assert.Equal(t, readFile(t, zshrc), "keep\n")
	// With the last finalizer removed, the App is deleted.
	var got appv1alpha1.App
	err = c.Get(context.Background(), client.ObjectKey{Name: appName}, &got)
	assert.Assert(t, err != nil)
}

// TestReconcileReleasesFinalizerWhenUnwindFails checks that a dotfile the user
// hand-edited into an unparseable state (only one marker survives) doesn't
// strand App deletion: the unwind fails, but the finalizer is released anyway.
func TestReconcileReleasesFinalizerWhenUnwindFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))
	r := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	// The end marker is gone, so managedlines.Manage errors on every attempt.
	zshrc := filepath.Join(home, ".zshrc")
	assert.NilError(t, os.WriteFile(zshrc,
		[]byte("# mine\n"+r.startMarker()+"\nexport PATH=\"x:$PATH\"\n"), 0o644))

	now := metav1.NewTime(time.Now())
	app := &appv1alpha1.App{
		Name:              appName,
		Generation:        1,
		DeletionTimestamp: &now,
		Finalizers:        []string{pathManagementFinalizer},
		Spec:              appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r.Client = c

	// Reconcile doesn't propagate the unwind error, so deletion isn't retried
	// forever on a file we can't repair.
	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	// The finalizer is gone, so the App can be deleted despite the broken file.
	var got appv1alpha1.App
	err = c.Get(context.Background(), client.ObjectKey{Name: appName}, &got)
	assert.Assert(t, err != nil)
}

// TestReconcileRetriesFinalizerWhenUnwindRepairable checks the other side: a
// repairable unwind failure (here a directory in place of ~/.zshrc, so
// managedlines.Manage can't read it) within the grace window keeps the finalizer
// and requeues, so the block isn't abandoned for a failure that a later pass
// could clear.
func TestReconcileRetriesFinalizerWhenUnwindRepairable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))
	r := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	// A directory in place of ~/.zshrc makes the read fail with an I/O error, not
	// a malformed-block error.
	assert.NilError(t, os.Mkdir(filepath.Join(home, ".zshrc"), 0o755))

	// Deletion requested just now, so we're inside the grace window.
	now := metav1.NewTime(time.Now())
	app := &appv1alpha1.App{
		Name:              appName,
		Generation:        1,
		DeletionTimestamp: &now,
		Finalizers:        []string{pathManagementFinalizer},
		Spec:              appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r.Client = c

	// Within the grace window the reconcile requeues (no error, RequeueAfter set)
	// rather than releasing.
	res, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)
	assert.Assert(t, res.RequeueAfter > 0)

	// The finalizer is still there, so the App isn't deleted while the block might
	// still be removable on a later pass.
	var got appv1alpha1.App
	assert.NilError(t, c.Get(context.Background(), client.ObjectKey{Name: appName}, &got))
	assert.Assert(t, controllerutil.ContainsFinalizer(&got, pathManagementFinalizer))
}

// TestReconcileReleasesFinalizerAfterUnwindGrace checks the bound: a repairable
// unwind failure that never clears (here a permanently unreadable ~/.zshrc) must
// not strand the App forever. Once the grace window measured from
// deletionTimestamp has passed, the finalizer is released anyway.
func TestReconcileReleasesFinalizerAfterUnwindGrace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))
	r := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	assert.NilError(t, os.Mkdir(filepath.Join(home, ".zshrc"), 0o755))

	// Deletion was requested well before the grace window; the retries are spent.
	old := metav1.NewTime(time.Now().Add(-2 * pathUnwindRetryGrace))
	app := &appv1alpha1.App{
		Name:              appName,
		Generation:        1,
		DeletionTimestamp: &old,
		Finalizers:        []string{pathManagementFinalizer},
		Spec:              appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r.Client = c

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	// The finalizer is gone, so the App can finish deleting despite the file we
	// can't read.
	var got appv1alpha1.App
	err = c.Get(context.Background(), client.ObjectKey{Name: appName}, &got)
	assert.Assert(t, err != nil)
}

func TestReconcileDoesNotReapplyWhileDeleting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))

	zshrc := filepath.Join(home, ".zshrc")
	assert.NilError(t, os.WriteFile(zshrc, []byte("keep\n"), 0o644))

	now := metav1.NewTime(time.Now())
	app := &appv1alpha1.App{
		Name:              appName,
		Generation:        1,
		DeletionTimestamp: &now,
		// A second finalizer keeps the App around after we drop ours, so we
		// can still observe that no block was written.
		Finalizers: []string{pathManagementFinalizer, "rdd.rancherdesktop.io/cleanup"},
		Spec:       appv1alpha1.AppSpec{Application: appv1alpha1.ApplicationSpec{AddPath: appv1alpha1.AddPathFront}},
	}
	scheme := reconcilerTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &PathManagementReconciler{Client: c, BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}

	_, err := r.Reconcile(context.Background(), ctrl.Request{})
	assert.NilError(t, err)

	got := readFile(t, zshrc)
	assert.Assert(t, !strings.Contains(got, r.startMarker()))
}

// TestApplyConcurrentInstancesKeepBothBlocks checks that two instances editing
// the same ~/.zshrc at once don't lose each other's block. The fence markers are
// suffix-tagged so the instances can share the file, but managedlines.Manage does
// read-compute-write, so without the cross-process lock the later writer computes
// from a stale read and drops the earlier block. apply holds the lock across the
// whole edit, so both blocks survive.
func TestApplyConcurrentInstancesKeepBothBlocks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg-config-home"))

	r2 := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd2", "bin"), Suffix: "2"}
	r3 := &PathManagementReconciler{BinDir: filepath.Join(home, ".rd3", "bin"), Suffix: "3"}
	zshrc := filepath.Join(home, ".zshrc")

	// The overlap that loses a block is timing-dependent, so repeat: resetting the
	// file each round makes a regression fail reliably instead of flaking green.
	for range 30 {
		assert.NilError(t, os.WriteFile(zshrc, []byte("# mine\n"), 0o644))

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, r := range []*PathManagementReconciler{r2, r3} {
			wg.Add(1)
			go func(i int, r *PathManagementReconciler) {
				defer wg.Done()
				<-start // release both at once to force the overlap
				errs[i] = r.apply(context.Background(), appv1alpha1.AddPathFront, false)
			}(i, r)
		}
		close(start)
		wg.Wait()

		assert.NilError(t, errs[0])
		assert.NilError(t, errs[1])

		got := readFile(t, zshrc)
		assert.Assert(t, strings.Contains(got, r2.startMarker()), "instance 2 block missing: %q", got)
		assert.Assert(t, strings.Contains(got, r3.startMarker()), "instance 3 block missing: %q", got)
	}
}

// TestPermanentPathError checks the classifier that decides whether an apply
// failure is permanent (a malformed block only the user can fix) or repairable
// (I/O). It treats a joined error as permanent only when every cause is, so one
// transient failure among several files keeps the whole thing repairable.
func TestPermanentPathError(t *testing.T) {
	malformed := fmt.Errorf(".zshrc: %w", managedlines.ErrMalformedBlock)
	ioErr := errors.New("open .bashrc: permission denied")

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil is not permanent", nil, false},
		{"a bare I/O error is repairable", ioErr, false},
		{"a malformed error is permanent", malformed, true},
		{"all-malformed join is permanent", errors.Join(malformed, malformed), true},
		{"a join with one I/O error is repairable", errors.Join(malformed, ioErr), false},
		{"a join of only I/O errors is repairable", errors.Join(ioErr, ioErr), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, permanentPathError(tc.err), tc.want)
		})
	}
}
