// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

const (
	// hwndBroadcast sends the message to all top-level windows.
	hwndBroadcast = windows.HWND(0xffff)
	// wmSettingChange notifies applications that a system-wide setting changed.
	wmSettingChange = 0x001A
	// smtoAbortIfHung skips windows that have stopped responding instead of
	// blocking on them.
	smtoAbortIfHung = 0x0002
)

// Loaded once and shared across broadcasts. LazyDLL/LazyProc resolve on first
// use, so declaring them here costs nothing if we never broadcast.
var (
	user32             = windows.NewLazySystemDLL("user32.dll")
	sendMessageTimeout = user32.NewProc("SendMessageTimeoutA")
)

// envSubKey is the HKCU subkey that holds the user Path. It's a var so tests can
// point at a throwaway key instead of the real one.
var envSubKey = `Environment`

// pathMarkerExists reports whether the PATH marker is present. Windows Path has
// no fenced block to mark our entry, so under manual we consult the marker to
// tell an entry we added from one the user typed. Present means remove the
// entry from any position; absent means leave it alone.
func (r *PathManagementReconciler) pathMarkerExists() bool {
	_, err := os.Stat(r.PathMarker)
	return err == nil
}

// writePathMarker records that rdd owns the bin directory entry. front/back call
// it before the registry write, so a crash in between still leaves the marker
// and a later manual run can remove the entry.
func (r *PathManagementReconciler) writePathMarker() error {
	marker := r.PathMarker
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return fmt.Errorf("create PATH marker directory: %w", err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return fmt.Errorf("write PATH marker %q: %w", marker, err)
	}
	return nil
}

// removePathMarker deletes the marker, treating an already-absent marker as
// success.
func (r *PathManagementReconciler) removePathMarker() error {
	if err := os.Remove(r.PathMarker); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove PATH marker: %w", err)
	}
	return nil
}

// applyPath edits the user's Path in HKCU\Environment and, if it changed,
// broadcasts WM_SETTINGCHANGE so new processes (and refresh-aware apps) pick it
// up. forceRemove drops our entry regardless of position (used when unwinding
// for deletion); see computeWindowsPath.
//
// The caller (apply) holds a cross-process lock, so the read-modify-write of the
// user Path can't lose a concurrent instance's entry.
func (r *PathManagementReconciler) applyPath(_ context.Context, strategy appv1alpha1.AddPathStrategy, forceRemove bool) error {
	changed, err := r.writeUserPath(strategy, forceRemove)
	if err != nil {
		return err
	}
	if changed {
		broadcastEnvironmentChange()
	}
	return nil
}

// writeUserPath applies strategy to the user Path in the registry and reports
// whether it changed. It's separate from apply, and skips the broadcast, so a
// test can run it against a throwaway key.
//
// The marker file tracks ownership across runs: front/back write it before the
// registry edit, and manual removes the entry only when the marker (or
// forceRemove) says it's ours, then clears the marker.
func (r *PathManagementReconciler) writeUserPath(strategy appv1alpha1.AddPathStrategy, forceRemove bool) (bool, error) {
	adding := strategy == appv1alpha1.AddPathFront || strategy == appv1alpha1.AddPathBack

	// Record ownership before the registry write, so a crash in between still
	// leaves the marker for a later manual run to act on.
	if adding {
		if err := r.writePathMarker(); err != nil {
			return false, err
		}
	}

	// Under manual, the marker decides whether the entry is ours to remove;
	// forceRemove (deletion unwind) removes it regardless of the marker.
	removeExisting := forceRemove || (!adding && r.pathMarkerExists())

	key, _, err := registry.CreateKey(registry.CURRENT_USER, envSubKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return false, fmt.Errorf("open HKCU\\%s: %w", envSubKey, err)
	}
	defer key.Close()

	current, valType, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return false, fmt.Errorf("read user Path: %w", err)
	}

	updated, changed := computeWindowsPath(current, r.BinDir, strategy, removeExisting)
	if changed {
		// Keep the existing value type. The user Path is usually REG_EXPAND_SZ (it
		// can contain things like %USERPROFILE%), so use that when it did not exist
		// yet.
		if valType == registry.SZ {
			err = key.SetStringValue("Path", updated)
		} else {
			err = key.SetExpandStringValue("Path", updated)
		}
		if err != nil {
			return false, fmt.Errorf("write user Path: %w", err)
		}
	}

	// Once the entry is removed (manual with the marker, or forceRemove), clear
	// the marker so a fresh entry the user later adds is left alone. Do it after
	// the registry write so a crash before the write leaves the marker for the
	// retry; clear it even when the Path didn't change, to drop a stale marker
	// whose entry the user had already removed by hand.
	if !adding && removeExisting {
		if err := r.removePathMarker(); err != nil {
			return changed, err
		}
	}

	return changed, nil
}

// broadcastEnvironmentChange tells running applications the environment changed.
// Errors are ignored: the Path is already written and new processes pick it up.
func broadcastEnvironmentChange() {
	env, err := windows.BytePtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = sendMessageTimeout.Call(
		uintptr(hwndBroadcast),
		wmSettingChange,
		0,
		uintptr(unsafe.Pointer(env)),
		smtoAbortIfHung,
		5000,
		uintptr(unsafe.Pointer(&result)),
	)
}
