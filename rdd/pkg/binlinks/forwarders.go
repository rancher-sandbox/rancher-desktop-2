// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package binlinks

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/klog/v2"
)

// Windows cannot symlink without privileges, and a standard user cannot even
// hardlink a binary under Program Files (CreateHardLink needs WRITE_ATTRIBUTES
// on the target). So on Windows the bundle publishes a small forwarder for each
// tool: a copy the user owns, beside a <tool>.shim file naming the real binary
// to exec.
//
// Contract with the forwarder binary (vendored in rdd/src/rdd-forwarder),
// all following three must stay in sync with it:
//   - it ships in the bundle bin directory as forwarderName;
//   - it reads its target from a sibling file named <own basename>.shim, in the
//     shimContent format below;
//   - when it execs the target it passes through the name it was invoked as
//     (argv[0]), rather than the target's name. rdd is a multicall binary that
//     switches behaviour on that name, so kubectl.exe forwarding to rdd.exe must
//     still present as "kubectl" for rdd to act as kubectl and not the daemon.
const (
	forwarderName = "rdd-forwarder.exe"
	shimSuffix    = ".shim"
)

// shimContent is the body of a <tool>.shim file: the upstream shim format,
// naming the binary the forwarder should exec.
func shimContent(target string) []byte {
	return []byte("path = " + target + "\r\n")
}

// shimPath is the .shim file for a forwarder named tool (tool keeps its .exe
// suffix, the shim drops it: kubectl.exe -> kubectl.shim).
func shimPath(binDir, tool string) string {
	return filepath.Join(binDir, strings.TrimSuffix(tool, ".exe")+shimSuffix)
}

// linkForwarders publishes a forwarder per bundled tool into binDir, and a
// forwarder per bundled docker CLI plugin into both binDir (so the plugins also
// work as standalone commands) and pluginDir (which holds only plugins, for
// docker's cliPluginsExtraDirs). It does not wipe either directory: the running
// rdd reaches this code through its own forwarder, and Windows will not delete a
// running executable. Instead it reconciles — refresh each .shim, point each
// name at the bin directory's forwarder copy, and drop forwarders the bundle no
// longer ships. Plugin-directory forwarders hardlink to the bin directory's
// copy; both directories are on the same volume.
func linkForwarders(execPath, binDir, pluginDir string) error {
	srcDir := filepath.Dir(execPath)
	forwarderSrc := filepath.Join(srcDir, forwarderName)
	if _, err := os.Stat(forwarderSrc); err != nil {
		return fmt.Errorf("locate forwarder %q: %w", forwarderSrc, err)
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read bundle directory %q: %w", srcDir, err)
	}
	plugins := pluginTargets(execPath)

	// Every bundled binary forwards to itself; kubectl is a multicall into rdd
	// rather than a separate binary, so it forwards to rdd. The plugins also go
	// into the bin directory so they work as standalone commands.
	binWant := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == forwarderName {
			continue
		}
		binWant[entry.Name()] = filepath.Join(srcDir, entry.Name())
	}
	binWant["kubectl.exe"] = filepath.Join(srcDir, "rdd.exe")
	for name, target := range plugins {
		binWant[name] = target
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", binDir, err)
	}

	master := filepath.Join(binDir, forwarderName)
	if err := ensureForwarderCopy(forwarderSrc, master); err != nil {
		return fmt.Errorf("stage forwarder %q: %w", master, err)
	}

	// Bin directory: match #448's reconcile (publish, drop no-longer-bundled,
	// sweep), without the dangling-target prune, which the standalone path owns.
	publishForwarders(binDir, master, binWant)
	removeStaleForwarders(binDir, binWant)
	sweepAside(binDir)

	// Plugin directory: the same, plus a dangling-target prune so a plugin whose
	// bundle target moved does not shadow one docker finds in another directory.
	publishForwarders(pluginDir, master, plugins)
	removeStaleForwarders(pluginDir, plugins)
	pruneDanglingForwarders(pluginDir)
	sweepAside(pluginDir)
	return nil
}

// publishForwarders writes a .shim for each want entry into dir and hardlinks
// its name to master. dir need not hold master: a plugin-directory forwarder
// hardlinks to the bin directory's copy, on the same volume. Per-entry failures
// are logged and skipped.
func publishForwarders(dir, master string, want map[string]string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		klog.Warningf("Failed to create %q: %v", dir, err)
		return
	}
	for tool, target := range want {
		if err := os.WriteFile(shimPath(dir, tool), shimContent(target), 0o644); err != nil {
			klog.Warningf("Failed to write shim for %q in %q, skipping: %v", tool, dir, err)
			continue
		}
		if err := ensureForwarderLink(master, filepath.Join(dir, tool)); err != nil {
			klog.Warningf("Failed to link forwarder %q in %q, skipping: %v", tool, dir, err)
		}
	}
}

// ensureForwarderCopy makes master a copy of the bundled forwarder. An existing
// copy with the same bytes is kept so its tool hardlinks stay valid; otherwise a
// running copy is renamed aside (Windows forbids deleting it but allows the
// rename) and replaced.
func ensureForwarderCopy(src, master string) error {
	want, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(master); err == nil && bytes.Equal(cur, want) {
		return nil
	}
	if err := renameAside(master); err != nil {
		return err
	}
	return os.WriteFile(master, want, 0o755)
}

// ensureForwarderLink makes linkPath a hardlink to master. A link already
// sharing master's file is kept; any other entry is renamed aside first, since a
// running forwarder cannot be deleted but can be renamed.
func ensureForwarderLink(master, linkPath string) error {
	if cur, err := os.Stat(linkPath); err == nil {
		if m, err := os.Stat(master); err == nil && os.SameFile(cur, m) {
			return nil
		}
	}
	if err := renameAside(linkPath); err != nil {
		return err
	}
	return os.Link(master, linkPath)
}

// removeStaleForwarders drops forwarders and their shims for tools no longer in
// want, so an uninstalled tool cannot linger on PATH. The master copy has no
// shim and is left in place.
func removeStaleForwarders(binDir string, want map[string]string) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		klog.Warningf("Failed to read %q while removing stale forwarders: %v", binDir, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, shimSuffix) {
			continue
		}
		tool := strings.TrimSuffix(name, shimSuffix) + ".exe"
		if _, ok := want[tool]; ok {
			continue
		}
		dropForwarder(binDir, tool)
	}
}

// pruneDanglingForwarders drops any forwarder whose shim names a target that no
// longer exists, so a forwarder left by an uninstalled application cannot shadow
// a tool the user later installs on PATH. Directories without shims (non-Windows
// bin directories) yield nothing.
func pruneDanglingForwarders(binDir string) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		klog.Warningf("Failed to read %q while pruning forwarders: %v", binDir, err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, shimSuffix) {
			continue
		}
		target := shimTarget(filepath.Join(binDir, name))
		if target == "" {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			continue
		}
		dropForwarder(binDir, strings.TrimSuffix(name, shimSuffix)+".exe")
	}
}

// shimTarget returns the path named by a .shim file, or "" if it cannot be read
// or parsed.
func shimTarget(shim string) string {
	data, err := os.ReadFile(shim)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "path"); ok {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), "="))
		}
	}
	return ""
}

// dropForwarder removes a forwarder and its shim, renaming a running forwarder
// aside when it cannot be deleted so the next start can sweep it.
func dropForwarder(binDir, tool string) {
	if err := os.Remove(shimPath(binDir, tool)); err != nil && !os.IsNotExist(err) {
		klog.Warningf("Failed to remove shim for %q: %v", tool, err)
	}
	forwarder := filepath.Join(binDir, tool)
	if err := os.Remove(forwarder); err != nil && !os.IsNotExist(err) {
		if asideErr := renameAside(forwarder); asideErr != nil {
			klog.Warningf("Failed to remove forwarder %q: %v", tool, err)
		}
	}
}

// renameAside moves path to a sibling .old name so a running executable can be
// replaced, then is cleaned up by sweepAside on a later start. A missing path is
// a no-op.
func renameAside(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	aside := path + ".old"
	_ = os.Remove(aside)
	return os.Rename(path, aside)
}

// sweepAside deletes leftover .old files from earlier renames, skipping any that
// are still in use.
func sweepAside(binDir string) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".old") {
			_ = os.Remove(filepath.Join(binDir, entry.Name()))
		}
	}
}
