// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"sync"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/perf"
)

// startConditionTiming records cond.flip and cond.after_running for App and
// LimaVM objects from the manager's shared informers, so the status writers
// don't need timing code of their own. It skips a kind that is missing from
// the manager's scheme, because this manager's controllers don't use it. It
// also skips a kind whose CRD isn't installed, which happens when this process
// doesn't run the controller that installs the CRD.
func startConditionTiming(ctx context.Context, mgr ctrl.Manager) error {
	tracker := newConditionTracker()
	for _, obj := range []client.Object{&appv1alpha1.App{}, &limav1alpha1.LimaVM{}} {
		gvk, err := apiutil.GVKForObject(obj, mgr.GetScheme())
		if err != nil {
			continue
		}
		informer, err := mgr.GetCache().GetInformer(ctx, obj, cache.BlockUntilSynced(false))
		if apimeta.IsNoMatchError(err) {
			continue
		}
		if err != nil {
			return err
		}
		kind := gvk.Kind
		observe := func(obj any) {
			meta, conditions, ok := objectConditions(obj)
			if !ok {
				return
			}
			for _, f := range tracker.observe(time.Now(), kind, meta, conditions) {
				perf.Duration(ctx, "cond.flip", f.sinceGeneration,
					"kind", kind, "cond", f.cond, "status", f.status, "reason", f.reason)
				if f.hasAfterRunning {
					perf.Duration(ctx, "cond.after_running", f.afterRunning, "cond", f.cond)
				}
			}
		}
		_, err = informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
			AddFunc:    observe,
			UpdateFunc: func(_, newObj any) { observe(newObj) },
			DeleteFunc: func(obj any) {
				if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
					obj = tombstone.Obj
				}
				if meta, _, ok := objectConditions(obj); ok {
					tracker.forget(meta.GetUID())
				}
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// objectConditions returns the metadata and status conditions of an App or
// LimaVM.
func objectConditions(obj any) (metav1.Object, []metav1.Condition, bool) {
	switch o := obj.(type) {
	case *appv1alpha1.App:
		return o, o.Status.Conditions, true
	case *limav1alpha1.LimaVM:
		return o, o.Status.Conditions, true
	}
	return nil, nil, false
}

// conditionTracker remembers, per object, when its generation last changed
// (or when the tracker first saw the object) and each condition's last
// status and reason. The informer reports every write to the object,
// including writes that leave each condition's status and reason as they
// were, so the tracker counts a flip only when a status or reason changes.
type conditionTracker struct {
	mu      sync.Mutex
	objects map[types.UID]*objectTiming
}

type objectTiming struct {
	generation        int64
	generationChanged time.Time
	conditions        map[string]conditionState
}

type conditionState struct {
	status  metav1.ConditionStatus
	reason  string
	changed time.Time
}

// conditionFlip is one condition that changed status or reason.
type conditionFlip struct {
	cond, status, reason string
	sinceGeneration      time.Duration
	// afterRunning is the time since the App's Running condition turned True.
	// It is set only when an App's ContainerEngineReady or KubernetesReady
	// turns True while Running is True.
	afterRunning    time.Duration
	hasAfterRunning bool
}

func newConditionTracker() *conditionTracker {
	return &conditionTracker{objects: map[types.UID]*objectTiming{}}
}

// observe compares conditions with what the tracker last saw for obj and
// returns the flips. The first observation of an object sets the baseline and
// doesn't return any flips; a condition that appears later counts as a flip.
func (t *conditionTracker) observe(now time.Time, kind string, obj metav1.Object, conditions []metav1.Condition) []conditionFlip {
	t.mu.Lock()
	defer t.mu.Unlock()

	timing, known := t.objects[obj.GetUID()]
	if !known {
		timing = &objectTiming{
			generation:        obj.GetGeneration(),
			generationChanged: now,
			conditions:        map[string]conditionState{},
		}
		t.objects[obj.GetUID()] = timing
	} else if obj.GetGeneration() != timing.generation {
		timing.generation = obj.GetGeneration()
		timing.generationChanged = now
	}

	var flips []conditionFlip
	for _, c := range conditions {
		prev, seen := timing.conditions[c.Type]
		if seen && prev.status == c.Status && prev.reason == c.Reason {
			continue
		}
		timing.conditions[c.Type] = conditionState{status: c.Status, reason: c.Reason, changed: now}
		if !known {
			continue
		}
		f := conditionFlip{
			cond:            c.Type,
			status:          string(c.Status),
			reason:          c.Reason,
			sinceGeneration: now.Sub(timing.generationChanged),
		}
		if kind == "App" && c.Status == metav1.ConditionTrue &&
			(c.Type == appv1alpha1.AppConditionContainerEngineReady || c.Type == appv1alpha1.AppConditionKubernetesReady) {
			if running, ok := timing.conditions[appv1alpha1.AppConditionRunning]; ok && running.status == metav1.ConditionTrue {
				f.afterRunning = now.Sub(running.changed)
				f.hasAfterRunning = true
			}
		}
		flips = append(flips, f)
	}
	return flips
}

// forget drops the tracker's state for a deleted object.
func (t *conditionTracker) forget(uid types.UID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.objects, uid)
}
