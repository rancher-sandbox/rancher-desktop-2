// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConditionTracker(t *testing.T) {
	tracker := newConditionTracker()
	start := time.Now()
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	app := &metav1.ObjectMeta{UID: "app", Generation: 1}
	cond := func(typ string, status metav1.ConditionStatus, reason string) metav1.Condition {
		return metav1.Condition{Type: typ, Status: status, Reason: reason}
	}

	// The first observation sets the baseline.
	flips := tracker.observe(at(0), "App", app, []metav1.Condition{cond("Running", "False", "Stopped")})
	assert.Equal(t, len(flips), 0)

	// A generation change restarts the clock for later flips.
	app.Generation = 2
	flips = tracker.observe(at(100), "App", app, []metav1.Condition{cond("Running", "False", "Stopped")})
	assert.Equal(t, len(flips), 0)

	// A re-stamped condition with the same status and reason is not a flip.
	restamped := cond("Running", "False", "Stopped")
	restamped.LastTransitionTime = metav1.NewTime(at(200))
	flips = tracker.observe(at(200), "App", app, []metav1.Condition{restamped})
	assert.Equal(t, len(flips), 0)

	// A changed reason is a flip, timed from the generation change.
	flips = tracker.observe(at(300), "App", app, []metav1.Condition{cond("Running", "False", "Starting")})
	assert.Equal(t, len(flips), 1)
	assert.Equal(t, flips[0], conditionFlip{cond: "Running", status: "False", reason: "Starting", sinceGeneration: 200 * time.Millisecond})

	// An engine that is ready before Running turns True doesn't get an
	// after_running time.
	flips = tracker.observe(at(400), "App", app, []metav1.Condition{
		cond("Running", "False", "Starting"), cond("ContainerEngineReady", "True", "Ready"),
	})
	assert.Equal(t, len(flips), 1)
	assert.Assert(t, !flips[0].hasAfterRunning)

	flips = tracker.observe(at(500), "App", app, []metav1.Condition{
		cond("Running", "True", "Running"), cond("ContainerEngineReady", "False", "Waiting"),
	})
	assert.Equal(t, len(flips), 2)
	assert.Assert(t, !flips[1].hasAfterRunning)

	// ContainerEngineReady turning True after Running records the time since
	// Running turned True, and an unrelated condition doesn't get that time.
	flips = tracker.observe(at(1500), "App", app, []metav1.Condition{
		cond("Running", "True", "Running"), cond("ContainerEngineReady", "True", "Ready"), cond("Other", "True", "Ready"),
	})
	assert.Equal(t, len(flips), 2)
	assert.Equal(t, flips[0].cond, "ContainerEngineReady")
	assert.Assert(t, flips[0].hasAfterRunning)
	assert.Equal(t, flips[0].afterRunning, time.Second)
	assert.Equal(t, flips[0].sinceGeneration, 1400*time.Millisecond)
	assert.Equal(t, flips[1].cond, "Other")
	assert.Assert(t, !flips[1].hasAfterRunning)

	// Only an App gets after_running.
	vm := &metav1.ObjectMeta{UID: "vm", Generation: 1}
	tracker.observe(at(0), "LimaVM", vm, []metav1.Condition{cond("Running", "True", "Running")})
	flips = tracker.observe(at(100), "LimaVM", vm, []metav1.Condition{
		cond("Running", "True", "Running"), cond("KubernetesReady", "True", "Ready"),
	})
	assert.Equal(t, len(flips), 1)
	assert.Assert(t, !flips[0].hasAfterRunning)

	// A forgotten object starts over with a new baseline.
	tracker.forget(app.UID)
	flips = tracker.observe(at(2000), "App", app, []metav1.Condition{cond("Running", "False", "Stopped")})
	assert.Equal(t, len(flips), 0)
}
