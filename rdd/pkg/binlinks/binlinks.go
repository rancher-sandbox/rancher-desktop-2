// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package binlinks publishes the binaries bundled with the Rancher Desktop
// application into the instance bin directory (~/.rd<instance>/bin) as
// symlinks, so a user can put that directory on PATH. Inside the application
// bundle rdd owns the directory and recreates it to mirror the bundled
// binaries. Standalone, rdd repairs only its own rdd and kubectl links, and
// only when they are missing or dangling, so links the application installed
// survive and a CLI-only install still gets a usable rdd and kubectl.
package binlinks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// LinkBundledBinaries publishes rdd's binaries into the instance bin directory
// as symlinks. Inside the application bundle it recreates the directory to
// mirror every bundled binary; standalone it repairs only its own rdd and
// kubectl links. Publishing is best-effort: the returned error is for the
// caller to log and must not block startup.
func LinkBundledBinaries() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	binDir := filepath.Join(instance.ShortDir(), "bin")
	if inAppBundle(execPath, runtime.GOOS) {
		return linkBinaries(execPath, binDir)
	}
	return ensureSelfLinks(execPath, binDir)
}

// inAppBundle reports whether execPath is the bundled rdd binary for the given
// OS, as opposed to a standalone CLI install. The application stages its
// per-platform resources under <resources>/<goos>/bin/rdd, where the directory
// is "Resources" on macOS (the .app bundle convention) and lowercase
// "resources" elsewhere. The leading separator anchors the match, so
// an unrelated path ending in the same tail does not qualify.
func inAppBundle(execPath, goos string) bool {
	resources := "resources"
	if goos == "darwin" {
		resources = "Resources"
	}
	return strings.HasSuffix(execPath, "/"+resources+"/"+goos+"/bin/rdd")
}

// linkBinaries recreates binDir with symlinks to the bundled binaries and a
// kubectl link to rdd. Recreating it drops stale links from a previous install;
// reading the source directory before removing binDir keeps the existing links
// when the read fails.
func linkBinaries(execPath, binDir string) error {
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
		if err := os.Symlink(filepath.Join(srcDir, name), filepath.Join(binDir, name)); err != nil {
			return fmt.Errorf("link %q: %w", name, err)
		}
	}

	// No separate kubectl binary is bundled; rdd provides it. Link kubectl to
	// rdd so kubectl on PATH reaches rdd.
	if err := os.Symlink(execPath, filepath.Join(binDir, "kubectl")); err != nil {
		return fmt.Errorf("link kubectl: %w", err)
	}
	return nil
}

// ensureSelfLinks points the rdd and kubectl links in binDir at a standalone
// rdd, so the instance bin directory stays usable when no application bundle
// has published them. It repairs each link only when it is missing or dangling
// and leaves every other entry, including a working link, untouched.
func ensureSelfLinks(execPath, binDir string) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", binDir, err)
	}
	for _, name := range []string{"rdd", "kubectl"} {
		if err := ensureSelfLink(filepath.Join(binDir, name), execPath); err != nil {
			return err
		}
	}
	return nil
}

// ensureSelfLink points linkPath at target unless it already resolves to an
// existing file. A missing or dangling link is recreated; a working link
// survives, so a link the application installed to a still-present binary is
// left in place.
func ensureSelfLink(linkPath, target string) error {
	if _, err := os.Stat(linkPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %q: %w", linkPath, err)
	}
	// Symlink fails when the path already exists, so drop a dangling link first.
	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %q: %w", linkPath, err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("link %q: %w", linkPath, err)
	}
	return nil
}
