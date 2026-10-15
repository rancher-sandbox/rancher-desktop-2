// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/docker/cli/cli/config/configfile"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/dockerconfig"
)

// dockerPluginsMarkerName marks, in the instance directory, that rdd added its
// plugin directory to config.json's cliPluginsExtraDirs. config.json's array has
// no fence, so under manual we consult this marker to remove only an entry we
// added, mirroring the Windows Path marker.
const dockerPluginsMarkerName = ".docker-plugins-managed"

func (r *PathManagementReconciler) dockerPluginsMarkerPath() string {
	return filepath.Join(filepath.Dir(r.BinDir), dockerPluginsMarkerName)
}

func (r *PathManagementReconciler) dockerPluginsMarkerExists() bool {
	_, err := os.Stat(r.dockerPluginsMarkerPath())
	return err == nil
}

func (r *PathManagementReconciler) writeDockerPluginsMarker() error {
	marker := r.dockerPluginsMarkerPath()
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return fmt.Errorf("create instance directory: %w", err)
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return fmt.Errorf("write docker plugins marker %q: %w", marker, err)
	}
	return nil
}

func (r *PathManagementReconciler) removeDockerPluginsMarker() error {
	if err := os.Remove(r.dockerPluginsMarkerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove docker plugins marker: %w", err)
	}
	return nil
}

// applyDockerPlugins registers (front/back) or removes (manual, forceRemove) the
// instance's plugin directory in config.json's cliPluginsExtraDirs, so `docker`
// finds the bundled plugins. It mirrors the PATH edit's strategy and marker
// discipline but runs on every platform, since Docker never finds plugins via
// PATH. An empty PluginDir disables it; both production call sites set it.
func (r *PathManagementReconciler) applyDockerPlugins(ctx context.Context, strategy appv1alpha1.AddPathStrategy, forceRemove bool) error {
	if r.PluginDir == "" {
		return nil
	}

	adding := strategy == appv1alpha1.AddPathFront || strategy == appv1alpha1.AddPathBack

	// Record ownership before the config.json write, so a crash in between leaves
	// the marker for a later manual run.
	if adding {
		if err := r.writeDockerPluginsMarker(); err != nil {
			return err
		}
	}

	removeExisting := forceRemove || (!adding && r.dockerPluginsMarkerExists())
	// manual without our marker has nothing to do. Leave config.json unread, so a
	// file docker/cli rejects can't fail PATH management under the default setting.
	if !adding && !removeExisting {
		return nil
	}

	err := dockerconfig.Edit(ctx, func(cf *configfile.ConfigFile) (bool, error) {
		updated, changed := computePluginDirs(cf.CLIPluginsExtraDirs, r.PluginDir, strategy, removeExisting)
		if !changed {
			return false, nil
		}
		cf.CLIPluginsExtraDirs = updated
		return true, nil
	})
	if err != nil {
		return err
	}

	// Clear the marker after the write, even when nothing changed, to drop a
	// stale marker whose entry the user had already removed by hand.
	if !adding && removeExisting {
		if err := r.removeDockerPluginsMarker(); err != nil {
			return err
		}
	}

	return nil
}

// computePluginDirs returns the new cliPluginsExtraDirs after applying strategy,
// and whether it changed. front/back drop every existing pluginDir entry and
// re-add it at the chosen end; manual drops them only when removeExisting (marker
// present or force-remove). An empty result is nil so config.json's omitempty
// drops the key.
func computePluginDirs(current []string, pluginDir string, strategy appv1alpha1.AddPathStrategy, removeExisting bool) ([]string, bool) {
	adding := strategy == appv1alpha1.AddPathFront || strategy == appv1alpha1.AddPathBack
	removeOurs := adding || removeExisting

	kept := make([]string, 0, len(current)+1)
	for _, entry := range current {
		if removeOurs && pluginDirEqual(entry, pluginDir) {
			continue
		}
		kept = append(kept, entry)
	}

	switch strategy {
	case appv1alpha1.AddPathFront:
		kept = append([]string{pluginDir}, kept...)
	case appv1alpha1.AddPathBack:
		kept = append(kept, pluginDir)
	}

	if len(kept) == 0 {
		kept = nil
	}
	return kept, !slices.Equal(kept, current)
}

// pluginDirEqual reports whether two entries name the same directory, ignoring
// trailing separators and, on Windows, case and slash direction.
func pluginDirEqual(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.FromSlash(a), filepath.FromSlash(b))
	}
	return a == b
}
