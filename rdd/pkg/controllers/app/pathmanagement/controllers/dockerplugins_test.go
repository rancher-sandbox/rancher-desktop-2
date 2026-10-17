// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// newPluginTestReconciler points HOME (and DOCKER_CONFIG) at throwaway temp
// directories so applyDockerPlugins edits an isolated config.json and the
// cross-process lock and marker land under the temp HOME. Returns the
// reconciler and the config.json path.
func newPluginTestReconciler(t *testing.T) (r *PathManagementReconciler, configFile string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dockerDir := filepath.Join(home, ".docker")
	assert.NilError(t, os.MkdirAll(dockerDir, 0o700))
	t.Setenv("DOCKER_CONFIG", dockerDir)

	r = &PathManagementReconciler{
		BinDir:    filepath.Join(home, ".rd2", "bin"),
		PluginDir: filepath.Join(home, ".rd2", "docker-cli-plugins"),
		Suffix:    "2",
	}
	return r, filepath.Join(dockerDir, "config.json")
}

func readPluginDirs(t *testing.T, configFile string) []string {
	t.Helper()
	data, err := os.ReadFile(configFile)
	if os.IsNotExist(err) {
		return nil
	}
	assert.NilError(t, err)
	var cfg struct {
		CLIPluginsExtraDirs []string `json:"cliPluginsExtraDirs"`
	}
	assert.NilError(t, json.Unmarshal(data, &cfg))
	return cfg.CLIPluginsExtraDirs
}

func TestComputePluginDirs(t *testing.T) {
	const dir = "/home/me/.rd2/docker-cli-plugins"
	const other = "/opt/other/plugins"

	t.Run("front prepends and is idempotent", func(t *testing.T) {
		got, changed := computePluginDirs([]string{other}, dir, appv1alpha1.AddPathFront, false)
		assert.Assert(t, changed)
		assert.DeepEqual(t, got, []string{dir, other})

		// Re-applying front with our entry already in front is a no-op.
		got2, changed2 := computePluginDirs(got, dir, appv1alpha1.AddPathFront, false)
		assert.Assert(t, !changed2)
		assert.DeepEqual(t, got2, []string{dir, other})
	})

	t.Run("back appends", func(t *testing.T) {
		got, changed := computePluginDirs([]string{other}, dir, appv1alpha1.AddPathBack, false)
		assert.Assert(t, changed)
		assert.DeepEqual(t, got, []string{other, dir})
	})

	t.Run("front then back moves the entry", func(t *testing.T) {
		got, changed := computePluginDirs([]string{dir, other}, dir, appv1alpha1.AddPathBack, false)
		assert.Assert(t, changed)
		assert.DeepEqual(t, got, []string{other, dir})
	})

	t.Run("front dedups an existing duplicate", func(t *testing.T) {
		got, changed := computePluginDirs([]string{dir, other, dir}, dir, appv1alpha1.AddPathFront, false)
		assert.Assert(t, changed)
		assert.DeepEqual(t, got, []string{dir, other})
	})

	t.Run("manual without marker leaves entries alone", func(t *testing.T) {
		got, changed := computePluginDirs([]string{dir, other}, dir, appv1alpha1.AddPathManual, false)
		assert.Assert(t, !changed)
		assert.DeepEqual(t, got, []string{dir, other})
	})

	t.Run("manual with marker removes our entry", func(t *testing.T) {
		got, changed := computePluginDirs([]string{dir, other}, dir, appv1alpha1.AddPathManual, true)
		assert.Assert(t, changed)
		assert.DeepEqual(t, got, []string{other})
	})

	t.Run("removing the only entry yields nil so the key is dropped", func(t *testing.T) {
		got, changed := computePluginDirs([]string{dir}, dir, appv1alpha1.AddPathManual, true)
		assert.Assert(t, changed)
		assert.Assert(t, got == nil)
	})

	t.Run("manual with marker but no entry is a no-op", func(t *testing.T) {
		got, changed := computePluginDirs([]string{other}, dir, appv1alpha1.AddPathManual, true)
		assert.Assert(t, !changed)
		assert.DeepEqual(t, got, []string{other})
	})
}

func TestApplyDockerPluginsFrontWritesMarkerAndEntry(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)

	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathFront, false))

	assert.DeepEqual(t, readPluginDirs(t, configFile), []string{r.PluginDir})
	assert.Assert(t, r.dockerPluginsMarkerExists(), "front must write the marker")
}

func TestApplyDockerPluginsPreservesUserEntries(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)
	seed := []byte(`{"cliPluginsExtraDirs":["/opt/other/plugins"]}` + "\n")
	assert.NilError(t, os.WriteFile(configFile, seed, 0o600))

	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathBack, false))

	assert.DeepEqual(t, readPluginDirs(t, configFile), []string{"/opt/other/plugins", r.PluginDir})
}

func TestApplyDockerPluginsManualRemovesWhenMarkerPresent(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)

	// Add first (writes the marker and the entry).
	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathFront, false))
	assert.DeepEqual(t, readPluginDirs(t, configFile), []string{r.PluginDir})

	// manual removes it because the marker says it's ours, and clears the marker.
	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathManual, false))
	assert.Assert(t, readPluginDirs(t, configFile) == nil)
	assert.Assert(t, !r.dockerPluginsMarkerExists(), "manual must clear the marker")
}

func TestApplyDockerPluginsManualKeepsUserEntryWithoutMarker(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)
	// The user added our directory themselves; no marker exists.
	seed := []byte(`{"cliPluginsExtraDirs":["` + jsonEscape(r.PluginDir) + `"]}` + "\n")
	assert.NilError(t, os.WriteFile(configFile, seed, 0o600))

	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathManual, false))

	assert.DeepEqual(t, readPluginDirs(t, configFile), []string{r.PluginDir})
}

// TestApplyDockerPluginsManualWithoutMarkerSkipsConfig checks that manual with no
// marker never loads config.json, so a file docker/cli rejects cannot fail PATH
// management for a user who never opted in.
func TestApplyDockerPluginsManualWithoutMarkerSkipsConfig(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)
	seed := []byte(`{"auths":{"example.com":{"auth":"not-base64"}}}` + "\n")
	assert.NilError(t, os.WriteFile(configFile, seed, 0o600))

	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathManual, false))
}

func TestApplyDockerPluginsForceRemoveIgnoresMarker(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)
	seed := []byte(`{"cliPluginsExtraDirs":["` + jsonEscape(r.PluginDir) + `"]}` + "\n")
	assert.NilError(t, os.WriteFile(configFile, seed, 0o600))

	// forceRemove (deletion unwind) removes our entry even without a marker.
	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathManual, true))

	assert.Assert(t, readPluginDirs(t, configFile) == nil)
}

func TestApplyDockerPluginsNoPluginDirIsNoOp(t *testing.T) {
	r, configFile := newPluginTestReconciler(t)
	r.PluginDir = ""

	assert.NilError(t, r.applyDockerPlugins(context.Background(), appv1alpha1.AddPathFront, false))

	_, err := os.Stat(configFile)
	assert.Assert(t, os.IsNotExist(err), "no config.json should be written when PluginDir is empty")
}

// jsonEscape renders s as a JSON string body (without the surrounding quotes),
// so a Windows path's backslashes are escaped in the seed config.json.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
