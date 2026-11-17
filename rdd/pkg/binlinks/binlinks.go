// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package binlinks publishes the binaries bundled with the Rancher Desktop
// application into the instance bin directory ($HOME/.rd<instance>/bin), so a
// user can put that directory on PATH. It also publishes the bundled docker CLI
// plugins into that bin directory and into a plugins-only directory
// ($HOME/.rd<instance>/docker-cli-plugins) that the path-management controller
// adds to docker's cliPluginsExtraDirs.
//
// On macOS and Linux each entry is a symlink, or a hardlink where symlinks need
// absent privileges.  On Windows, where a standard user can make neither under
// Program Files, the bundle instead publishes a small forwarder per tool beside
// a <tool>.shim file naming the real binary (see forwarders.go).  Inside the
// application bundle rdd owns these directories and keeps them mirroring the
// bundled binaries.  Standalone, rdd repairs its own rdd and multicall links
// and prunes anything an uninstalled application left dangling (symlinks, and
// forwarders whose target is gone), so a stale entry cannot shadow a tool the
// user later installs on PATH; working links, forwarders with a live target,
// and other files survive.
//
// In a development build inside the source tree, we act as if we are bundled by
// using the binaries fetched by `yarn install`, as long as they are available.
package binlinks

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"k8s.io/klog/v2"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// multiCallLinks are the subcommands rdd also provides as multicall binaries
// (see cmd/rdd/multicall.go); each gets a link in the bin directory pointing
// at rdd.
var multiCallLinks = []string{"kubectl", "nerdctl"}

// pathOptions describe where things are.
type pathOptions struct {
	// execPath is the path to the running rdd executable.
	execPath string
	// srcDir is where the bundled binaries are located.
	srcDir string
	// binDir is the instance's bin directory; something like ~/.rd2/bin.
	binDir string
	// pluginDir is the instance's docker CLI plugins directory.
	pluginDir string
	// pluginsSrcDir is the source directory for docker CLI plugins.
	pluginsSrcDir string
}

// LinkBundledBinaries publishes rdd's binaries into the instance bin directory.
//
// Inside the application bundle it mirrors every bundled binary, recreating the
// directory on macOS and Linux and reconciling forwarders in place on Windows.
// In developer mode, running from a checkout, it does the same from the
// checkout's resources directory, so a developer gets a working bin directory
// and docker CLI plugins without a packaged app.  Otherwise, it is standalone;
// it repairs only its own rdd and multicall links.
//
// Publishing is best-effort and must not block startup.  A per-binary link
// failure is logged and skipped.  Only a whole-operation failure is returned
// for the caller to log, such as an unreadable bundle directory, an unwritable
// bin directory, or a forwarder the bundle lacks or rdd cannot copy.
func LinkBundledBinaries() error {
	var paths pathOptions
	var err error
	if paths.execPath, err = os.Executable(); err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	paths.binDir = instance.BinDir()
	paths.pluginDir = instance.DockerCliPluginsDir()
	exe := exeSuffix(runtime.GOOS)
	// RDD_NO_SYMLINKS forces hardlinks, so tests can exercise the fallback that
	// real systems hit when symlinks need absent privileges.
	useSymlink := os.Getenv("RDD_NO_SYMLINKS") == ""

	var selfOnly bool
	paths.srcDir, paths.pluginsSrcDir, selfOnly = resolveSrcDirs(paths.execPath, runtime.GOOS, developer.Mode())

	if selfOnly {
		return ensureSelfLinks(paths, exe, useSymlink)
	}

	if runtime.GOOS == "windows" {
		return linkForwarders(paths)
	}
	return linkBinaries(paths, exe, useSymlink)
}

// resolveSrcDirs determines where files should be copied from, relative to the
// given `rdd` executable path.  It returns the paths matching those that should
// be used in [pathOptions].  The returned boolean indicates whether only the
// rdd multicall binaries should be processed.
func resolveSrcDirs(execPath, goos string, developerMode bool) (srcDir, pluginsSrcDir string, selfOnly bool) {
	// developerMode is passed as an argument for testability; `developer.Mode` is
	// memoized.
	selfOnly = true
	switch {
	case inAppBundle(execPath, goos):
		srcDir = filepath.Dir(execPath)
		pluginsSrcDir = bundlePluginsSrcDir(execPath)
		selfOnly = false
	case developerMode:
		srcDir = checkoutBinSrcDir(execPath, goos)
		pluginsSrcDir = checkoutPluginsSrcDir(execPath, goos)
		required := []string{srcDir, pluginsSrcDir}
		if goos == "windows" {
			// On Windows, we also need `rdd-forwarder.exe` before using plugins.
			forwarder := filepath.Join(filepath.Dir(execPath), forwarderName)
			required = append(required, forwarder)
		}
		selfOnly = false
		for _, path := range required {
			if _, err := os.Stat(path); err != nil {
				selfOnly = true
				break
			}
		}
	}
	return srcDir, pluginsSrcDir, selfOnly
}

// bundlePluginsSrcDir returns the packaged app's docker CLI plugins directory,
// a sibling of the bin directory that holds execPath
// (resources/<os>/docker-cli-plugins).
func bundlePluginsSrcDir(execPath string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(execPath)), "docker-cli-plugins")
}

// checkoutRoot returns the repository root for a developer-mode rdd running
// from <repo>/rdd/bin/rdd(.exe) — the layout developer.Mode detects by finding
// ../cmd/rdd/main.go relative to the executable's directory (pkg/developer).
func checkoutRoot(execPath string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(execPath)))
}

// checkoutBinSrcDir returns the checkout's assembled bin directory
// (<repo>/resources/<goos>/bin), which is filled via `yarn postinstall` with
// third-party bundled tools (helm, docker, …). Unlike a packaged app, rdd and
// rdd-forwarder are not copied in here.
func checkoutBinSrcDir(execPath, goos string) string {
	return filepath.Join(checkoutRoot(execPath), "resources", goos, "bin")
}

// checkoutPluginsSrcDir returns the checkout's assembled docker CLI plugins
// directory (<repo>/resources/<goos>/docker-cli-plugins), which is filled via
// `yarn postinstall`, mirroring bundlePluginsSrcDir's packaged-app layout.
func checkoutPluginsSrcDir(execPath, goos string) string {
	return filepath.Join(checkoutRoot(execPath), "resources", goos, "docker-cli-plugins")
}

// pluginTargets maps each bundled docker CLI plugin's file name to its path in
// pluginsSrcDir.  It returns nil when pluginsSrcDir does not exist.
func pluginTargets(pluginsSrcDir string) map[string]string {
	entries, err := os.ReadDir(pluginsSrcDir)
	if err != nil {
		return nil
	}
	want := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		want[entry.Name()] = filepath.Join(pluginsSrcDir, entry.Name())
	}
	return want
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

// linkBinaries recreates binDir with links to the binaries in srcDir, the
// multicall links (and rdd itself) to execPath, and links to the docker CLI
// plugins in pluginsSrcDir; and recreates pluginDir with links to just those
// plugins (for docker's cliPluginsExtraDirs). Recreating each drops stale links
// from a previous install; reading the source directories before removing the
// targets keeps the existing links when a read fails.
func linkBinaries(paths pathOptions, exe string, useSymlink bool) error {
	entries, err := os.ReadDir(paths.srcDir)
	if err != nil {
		return fmt.Errorf("read bundle directory %q: %w", paths.srcDir, err)
	}
	plugins := pluginTargets(paths.pluginsSrcDir)

	if err := os.RemoveAll(paths.binDir); err != nil {
		return fmt.Errorf("remove %q: %w", paths.binDir, err)
	}
	if err := os.MkdirAll(paths.binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", paths.binDir, err)
	}

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != exe {
			continue
		}
		if err := link(filepath.Join(paths.srcDir, name), filepath.Join(paths.binDir, name), useSymlink); err != nil {
			klog.Warningf("Failed to link bundled binary %q, skipping: %v", name, err)
		}
	}

	// rdd provides kubectl and nerdctl. Link each multicall name to rdd so it
	// reaches rdd from PATH, unless srcDir ships a binary of that name.
	for _, name := range append([]string{"rdd"}, multiCallLinks...) {
		linkPath := filepath.Join(paths.binDir, name+exe)
		if _, err := os.Lstat(linkPath); err == nil {
			continue
		}
		if err := link(paths.execPath, linkPath, useSymlink); err != nil {
			klog.Warningf("Failed to link %q to rdd: %v", name, err)
		}
	}

	// Plugins go into the bin directory too, so docker-compose and docker-buildx
	// work as standalone commands.
	for name, target := range plugins {
		if err := link(target, filepath.Join(paths.binDir, name), useSymlink); err != nil {
			klog.Warningf("Failed to link plugin %q into bin directory, skipping: %v", name, err)
		}
	}

	// Plugin directory: only the plugins, so docker's cliPluginsExtraDirs entry
	// doesn't pick up the docker-credential helpers that share the bin directory.
	if err := os.RemoveAll(paths.pluginDir); err != nil {
		return fmt.Errorf("remove %q: %w", paths.pluginDir, err)
	}
	if err := os.MkdirAll(paths.pluginDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", paths.pluginDir, err)
	}
	for name, target := range plugins {
		if err := link(target, filepath.Join(paths.pluginDir, name), useSymlink); err != nil {
			klog.Warningf("Failed to link plugin %q into plugin directory, skipping: %v", name, err)
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
// bundle has published it. It first prunes symlinks and forwarders left dangling
// by an uninstalled application, then points the rdd and multicall links at a
// standalone rdd. Working links, forwarders with a live target, and other files
// survive.
func ensureSelfLinks(paths pathOptions, exe string, useSymlink bool) error {
	if err := os.MkdirAll(paths.binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", paths.binDir, err)
	}
	if err := pruneDanglingLinks(paths.binDir); err != nil {
		return err
	}
	// A bundle install publishes Windows forwarders instead of symlinks; prune
	// any whose target an uninstalled application took with it.
	pruneDanglingForwarders(paths.binDir)
	// docker runs the first plugin candidate it finds, so a dangling forwarder in
	// the plugin directory would hide a working plugin in a later directory.
	if _, err := os.Stat(paths.pluginDir); err == nil {
		pruneDanglingForwarders(paths.pluginDir)
	}
	for _, name := range append([]string{"rdd"}, multiCallLinks...) {
		if err := ensureSelfLink(filepath.Join(paths.binDir, name+exe), paths.execPath, useSymlink); err != nil {
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
