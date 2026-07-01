// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package wsl wraps the wsl.exe commands rdd uses to manage the WSL2 distros
// that back Lima instances on Windows. Every command is a no-op on
// non-Windows, where Lima creates no WSL2 distros.
package wsl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// wsl.exe can hang when the WSL subsystem is degraded, so each call is
// time-bounded. --unregister is slower than --terminate.
const (
	terminateTimeout  = 10 * time.Second
	unregisterTimeout = 30 * time.Second
)

// DistroName returns the WSL2 distro name Lima registers for instName. Lima's
// WSL2 driver names each distro "lima-<instance>".
func DistroName(instName string) string {
	return "lima-" + instName
}

// Terminate runs `wsl.exe --terminate` to shut the distro down, releasing the
// kernel state that can make a following --unregister deadlock. No-op on
// non-Windows.
func Terminate(ctx context.Context, distroName string) error {
	return run(ctx, terminateTimeout, "--terminate", distroName)
}

// Unregister runs `wsl.exe --unregister`, dropping the WSL2 registration and
// the distro's ext4.vhdx so Lima imports a fresh distro on the next start.
// Terminate the distro first. No-op on non-Windows.
func Unregister(ctx context.Context, distroName string) error {
	return run(ctx, unregisterTimeout, "--unregister", distroName)
}

func run(ctx context.Context, timeout time.Duration, verb, distroName string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	wslCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(wslCtx, "wsl.exe", verb, distroName)
	cmd.Env = append(os.Environ(), "WSL_UTF8=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if out := strings.TrimSpace(string(output)); out != "" {
			return fmt.Errorf("wsl.exe %s %q: %w: %s", verb, distroName, err, out)
		}
		return fmt.Errorf("wsl.exe %s %q: %w", verb, distroName, err)
	}
	return nil
}

// UnregisterInstanceDistros unregisters the WSL2 distro backing each Lima
// instance under limaHome, clearing the `wsl --list` registration before its
// directory is removed. It terminates each distro first, because `wsl
// --unregister` can deadlock on a distro that still holds kernel state. Both
// calls are best-effort and time-bounded, since wsl.exe can hang when the WSL
// subsystem is degraded. No-op on non-Windows.
//
// TODO: This reimplements cleanup the lima controller already owns, because
// `svc delete` stops the daemon before reaching here. Route it through the
// controller once the stop/delete hook (rancher-desktop-2#319) exists.
func UnregisterInstanceDistros(ctx context.Context, limaHome string) {
	if runtime.GOOS != "windows" {
		return
	}
	for _, distroName := range instanceDistroNames(limaHome) {
		// Terminating is a best-effort precursor — the distro is usually already
		// stopped — so log its failure at debug.
		if err := Terminate(ctx, distroName); err != nil {
			logrus.WithError(err).WithField("distro", distroName).Debug("wsl --terminate failed during delete")
		}

		// A failed unregister is the failure that matters: it leaves behind the
		// stale registration this cleanup exists to prevent, so log it at warn.
		// (A distro that was never imported also fails here, harmlessly.)
		if err := Unregister(ctx, distroName); err != nil {
			logrus.WithError(err).WithField("distro", distroName).Warn("Failed to unregister WSL2 distro; a stale registration may remain")
		}
	}
}

// instanceDistroNames returns the WSL2 distro name ("lima-<instance>") for each
// Lima instance directory under limaHome. It skips Lima's reserved bookkeeping
// directories (those prefixed with "_" or "."), which are never instances, the
// same way Lima's own store.Instances does. It returns nil when limaHome does
// not exist (no instances were ever created).
func instanceDistroNames(limaHome string) []string {
	entries, err := os.ReadDir(limaHome)
	if err != nil {
		if !os.IsNotExist(err) {
			logrus.WithError(err).Warn("Failed to read Lima instance directory for WSL2 cleanup")
		}
		return nil
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		names = append(names, DistroName(name))
	}
	return names
}
