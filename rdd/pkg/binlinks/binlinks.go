// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package binlinks publishes the binaries bundled with the Rancher Desktop
// application into the instance bin directory (~/.rd<instance>/bin), so a user
// can put that directory on PATH. Each entry is a symlink, or a hardlink where
// symlinks need privileges that are absent (Windows without developer mode).
// Inside the application bundle rdd owns the directory and recreates it to
// mirror the bundled binaries. Standalone, rdd repairs its own rdd and
// multicall links and prunes any symlink left dangling by an uninstalled
// application, so a stale link cannot shadow a tool the user later installs on
// PATH; working links and non-symlink entries survive.
package binlinks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"k8s.io/klog/v2"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// multiCallLinks are the subcommands rdd also provides as multicall binaries
// (see cmd/rdd/multicall.go); each gets a link in the bin directory pointing
// at rdd.
var multiCallLinks = []string{"kubectl", "nerdctl"}

// LinkBundledBinaries publishes rdd's binaries into the instance bin directory.
// Inside the application bundle it recreates the directory to mirror every
// bundled binary; standalone it repairs only its own rdd and multicall links.
// Publishing is best-effort and must not block startup. A per-binary link
// failure is logged and skipped; only a whole-operation failure — an unreadable
// bundle directory or an unwritable bin directory — is returned for the caller
// to log.
func LinkBundledBinaries() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	binDir := filepath.Join(instance.ShortDir(), "bin")
	exe := exeSuffix(runtime.GOOS)
	// RDD_NO_SYMLINKS forces hardlinks, so tests can exercise the fallback that
	// real systems hit when symlinks need absent privileges.
	useSymlink := os.Getenv("RDD_NO_SYMLINKS") == ""
	if inAppBundle(execPath, runtime.GOOS) {
		return linkBinaries(execPath, binDir, exe, useSymlink)
	}
	return ensureSelfLinks(execPath, binDir, exe, useSymlink)
}

// exeSuffix is the executable extension for goos: ".exe" on Windows, empty
// elsewhere. Bundled binaries carry it in their own names; only the links rdd
// invents (the multicall links, and the standalone rdd) need it appended.
func exeSuffix(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// inAppBundle reports whether execPath is the bundled rdd binary for the given
// OS, as opposed to a standalone CLI install. The application stages its
// per-platform resources under <resources>/<goos>/bin/rdd, where the directory
// is "Resources" on macOS (the .app bundle convention) and lowercase
// "resources" elsewhere, the separator is a backslash on Windows, and the
// binary carries a .exe suffix there. The leading separator anchors the match,
// so an unrelated path ending in the same tail does not qualify.
func inAppBundle(execPath, goos string) bool {
	resources := "resources"
	sep := "/"
	switch goos {
	case "darwin":
		resources = "Resources"
	case "windows":
		sep = `\`
	}
	tail := strings.Join([]string{resources, goos, "bin", "rdd" + exeSuffix(goos)}, sep)
	return strings.HasSuffix(execPath, sep+tail)
}

// linkBinaries recreates binDir with links to the bundled binaries and the
// multicall links to rdd. Recreating it drops stale links from a previous
// install; reading the source directory before removing binDir keeps the
// existing links when the read fails.
func linkBinaries(execPath, binDir, exe string, useSymlink bool) error {
	srcDir := filepath.Dir(execPath)
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read bundle directory %q: %w", srcDir, err)
	}

	if err := os.RemoveAll(binDir); err != nil {
		return fmt.Errorf("remove %q: %w", binDir, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", binDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if err := link(filepath.Join(srcDir, name), filepath.Join(binDir, name), useSymlink); err != nil {
			klog.Warningf("Failed to link bundled binary %q, skipping: %v", name, err)
		}
	}

	// No separate kubectl or nerdctl binary is bundled; rdd provides them.
	// Link each multicall name to rdd so it reaches rdd from PATH. A bundled
	// binary of the same name wins over the multicall link.
	for _, name := range multiCallLinks {
		linkPath := filepath.Join(binDir, name+exe)
		if _, err := os.Lstat(linkPath); err == nil {
			continue
		}
		if err := link(execPath, linkPath, useSymlink); err != nil {
			klog.Warningf("Failed to link %s to rdd: %v", name, err)
		}
	}
	return nil
}

// link points linkPath at target, preferring a symlink for its self-documenting
// target and falling back to a hardlink where symlinks need absent privileges
// (Windows without developer mode). useSymlink false skips straight to a
// hardlink. A hardlink stays current across app updates because rdd recreates
// these links on every start; it works only within one volume, so the
// cross-volume copy fallback is deferred (see #448).
func link(target, linkPath string, useSymlink bool) error {
	if useSymlink {
		err := os.Symlink(target, linkPath)
		if err == nil {
			return nil
		}
		// Falling back is expected where symlinks need absent privileges, so log
		// the cause at a verbose level instead of warning on every such link.
		klog.V(1).Infof("symlink %q -> %q failed, falling back to a hardlink: %v", linkPath, target, err)
	}
	return os.Link(target, linkPath)
}

// ensureSelfLinks keeps the instance bin directory usable when no application
// bundle has published it. It first prunes symlinks left dangling by an
// uninstalled application, then points the rdd and multicall links at a
// standalone rdd. Working links and non-symlink entries survive.
func ensureSelfLinks(execPath, binDir, exe string, useSymlink bool) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", binDir, err)
	}
	if err := pruneDanglingLinks(binDir); err != nil {
		return err
	}
	for _, name := range append([]string{"rdd"}, multiCallLinks...) {
		if err := ensureSelfLink(filepath.Join(binDir, name+exe), execPath, useSymlink); err != nil {
			klog.Warningf("Failed to repair the %q link: %v", name+exe, err)
		}
	}
	return nil
}

// pruneDanglingLinks removes symlinks in binDir whose target no longer resolves,
// so a link left dangling by an uninstalled application cannot shadow a tool the
// user later installs elsewhere on PATH; zsh stops at the broken link instead of
// searching on. Working links and non-symlink entries stay. A hardlink from an
// uninstalled application is not dangling and survives, so it still shadows;
// detecting that needs the app install location (see #448).
//
// An entry it cannot stat or remove is logged and skipped, so one failure does
// not abort pruning the rest.
func pruneDanglingLinks(binDir string) error {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("read %q: %w", binDir, err)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		linkPath := filepath.Join(binDir, entry.Name())
		if _, err := os.Stat(linkPath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			klog.Warningf("Cannot stat %q while pruning dangling links, skipping: %v", linkPath, err)
			continue
		}
		if err := os.Remove(linkPath); err != nil {
			klog.Warningf("Failed to remove dangling link %q: %v", linkPath, err)
		}
	}
	return nil
}

// ensureSelfLink points linkPath at target unless it already resolves to an
// existing file. A missing or dangling link is recreated; a working link
// survives, so a link the application installed to a still-present binary is
// left in place.
//
// This detects an uninstalled app only for symlinks: a symlink dangles once its
// target is removed, while a hardlink keeps the inode alive and stays a valid
// file, so an orphaned hardlink survives and leaves the user on the old binary.
// Detecting that needs the app install location, not the link alone (see #448).
func ensureSelfLink(linkPath, target string, useSymlink bool) error {
	if _, err := os.Stat(linkPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %q: %w", linkPath, err)
	}
	// Linking fails when the path already exists, so drop a dangling link first.
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %q: %w", linkPath, err)
	}
	if err := link(target, linkPath, useSymlink); err != nil {
		return fmt.Errorf("link %q: %w", linkPath, err)
	}
	return nil
}
