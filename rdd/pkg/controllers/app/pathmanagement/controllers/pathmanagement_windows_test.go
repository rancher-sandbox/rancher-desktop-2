// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package controllers

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
	"gotest.tools/v3/assert"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// withTestEnvKey points envSubKey at a throwaway HKCU key so tests stay away
// from the real user Path, seeding it with the given value and type. It returns
// the key path and registers cleanup.
func withTestEnvKey(t *testing.T, seed string, seedType uint32) string {
	t.Helper()
	keyPath := fmt.Sprintf(`Software\rancher-desktop-daemon-test\%d`, time.Now().UnixNano())

	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE|registry.SET_VALUE)
	assert.NilError(t, err)
	if seed != "" {
		if seedType == registry.SZ {
			assert.NilError(t, key.SetStringValue("Path", seed))
		} else {
			assert.NilError(t, key.SetExpandStringValue("Path", seed))
		}
	}
	assert.NilError(t, key.Close())

	old := envSubKey
	envSubKey = keyPath
	t.Cleanup(func() {
		envSubKey = old
		_ = registry.DeleteKey(registry.CURRENT_USER, keyPath)
	})
	return keyPath
}

func readTestPath(t *testing.T, keyPath string) (value string, valType uint32) {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER, keyPath, registry.QUERY_VALUE)
	assert.NilError(t, err)
	defer key.Close()
	val, valType, err := key.GetStringValue("Path")
	assert.NilError(t, err)
	return val, valType
}

// newTestReconciler returns a reconciler with BinDir and the PATH marker in a
// fresh temp directory, so tests write the marker somewhere disposable.
func newTestReconciler(t *testing.T) *PathManagementReconciler {
	t.Helper()
	dir := t.TempDir()
	return &PathManagementReconciler{
		BinDir:     filepath.Join(dir, "bin"),
		PathMarker: filepath.Join(dir, "marker"),
	}
}

func assertMarkerExists(t *testing.T, r *PathManagementReconciler) {
	t.Helper()
	assert.Assert(t, r.pathMarkerExists(), "PATH marker should exist at %q", r.PathMarker)
}

func assertMarkerAbsent(t *testing.T, r *PathManagementReconciler) {
	t.Helper()
	assert.Assert(t, !r.pathMarkerExists(), "PATH marker should be absent at %q", r.PathMarker)
}

func TestWriteUserPathFrontPreservesExpandSZ(t *testing.T) {
	keyPath := withTestEnvKey(t, `%SystemRoot%\system32;C:\Windows`, registry.EXPAND_SZ)
	r := newTestReconciler(t)

	changed, err := r.writeUserPath(appv1alpha1.AddPathFront, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, true)

	val, valType := readTestPath(t, keyPath)
	assert.Equal(t, val, r.BinDir+`;%SystemRoot%\system32;C:\Windows`)
	// REG_EXPAND_SZ has to survive the rewrite, or entries like %SystemRoot%
	// stop expanding.
	assert.Equal(t, valType, uint32(registry.EXPAND_SZ))
	// front records ownership so a later manual run can remove the entry.
	assertMarkerExists(t, r)
}

func TestWriteUserPathKeepsPlainSZ(t *testing.T) {
	keyPath := withTestEnvKey(t, `C:\Windows`, registry.SZ)
	r := newTestReconciler(t)

	changed, err := r.writeUserPath(appv1alpha1.AddPathBack, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, true)

	val, valType := readTestPath(t, keyPath)
	assert.Equal(t, val, `C:\Windows;`+r.BinDir)
	assert.Equal(t, valType, uint32(registry.SZ))
	assertMarkerExists(t, r)
}

func TestWriteUserPathManualRemovesWithMarker(t *testing.T) {
	r := newTestReconciler(t)
	keyPath := withTestEnvKey(t, r.BinDir+`;C:\Windows`, registry.EXPAND_SZ)
	// The marker says rdd added the entry, so manual removes it.
	assert.NilError(t, r.writePathMarker())

	changed, err := r.writeUserPath(appv1alpha1.AddPathManual, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, true)

	val, _ := readTestPath(t, keyPath)
	assert.Equal(t, val, `C:\Windows`)
	// Removing our entry clears the marker.
	assertMarkerAbsent(t, r)
}

func TestWriteUserPathManualWithoutMarkerLeavesEntry(t *testing.T) {
	r := newTestReconciler(t)
	seed := r.BinDir + `;C:\Windows`
	keyPath := withTestEnvKey(t, seed, registry.EXPAND_SZ)
	// No marker: the user put the entry there, so manual leaves it alone.

	changed, err := r.writeUserPath(appv1alpha1.AddPathManual, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, false)

	val, _ := readTestPath(t, keyPath)
	assert.Equal(t, val, seed)
}

func TestWriteUserPathManualDeletesStaleMarker(t *testing.T) {
	r := newTestReconciler(t)
	// The marker is present, but the user already removed the entry by hand.
	keyPath := withTestEnvKey(t, `C:\Windows`, registry.EXPAND_SZ)
	assert.NilError(t, r.writePathMarker())

	changed, err := r.writeUserPath(appv1alpha1.AddPathManual, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, false)

	val, _ := readTestPath(t, keyPath)
	assert.Equal(t, val, `C:\Windows`)
	// A stale marker is cleared even when the Path didn't change.
	assertMarkerAbsent(t, r)
}

func TestWriteUserPathForceRemovesMiddleEntry(t *testing.T) {
	r := newTestReconciler(t)
	seed := `C:\Windows;` + r.BinDir + `;C:\Windows\System32`
	keyPath := withTestEnvKey(t, seed, registry.EXPAND_SZ)

	// Without the marker, manual leaves even a middle entry alone.
	changed, err := r.writeUserPath(appv1alpha1.AddPathManual, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, false)

	// The unwind path force-removes it regardless of the marker.
	changed, err = r.writeUserPath(appv1alpha1.AddPathManual, true)
	assert.NilError(t, err)
	assert.Equal(t, changed, true)

	val, _ := readTestPath(t, keyPath)
	assert.Equal(t, val, `C:\Windows;C:\Windows\System32`)
	assertMarkerAbsent(t, r)
}

func TestWriteUserPathIdempotent(t *testing.T) {
	r := newTestReconciler(t)
	withTestEnvKey(t, r.BinDir+`;C:\Windows`, registry.EXPAND_SZ)

	changed, err := r.writeUserPath(appv1alpha1.AddPathFront, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, false)
	// front still records ownership even when Path was already correct.
	assertMarkerExists(t, r)
}

func TestWriteUserPathCreatesWhenMissing(t *testing.T) {
	keyPath := withTestEnvKey(t, "", registry.EXPAND_SZ)
	r := newTestReconciler(t)

	changed, err := r.writeUserPath(appv1alpha1.AddPathFront, false)
	assert.NilError(t, err)
	assert.Equal(t, changed, true)

	val, valType := readTestPath(t, keyPath)
	assert.Equal(t, val, r.BinDir)
	// A brand new value comes out as REG_EXPAND_SZ.
	assert.Equal(t, valType, uint32(registry.EXPAND_SZ))
	assertMarkerExists(t, r)
}
