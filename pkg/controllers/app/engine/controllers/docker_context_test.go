// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

// dockerTestPaths holds the config and contexts directory for a test.
type dockerTestPaths struct {
	configFile  string
	contextsDir string
}

// newDockerTestDir creates a temp ~/.docker layout and returns its paths.
func newDockerTestDir(t *testing.T) dockerTestPaths {
	t.Helper()
	root := t.TempDir()
	dockerDir := filepath.Join(root, ".docker")
	assert.NilError(t, os.MkdirAll(dockerDir, 0o700))
	return dockerTestPaths{
		configFile:  filepath.Join(dockerDir, "config.json"),
		contextsDir: filepath.Join(dockerDir, "contexts"),
	}
}

func Test_updateDockerConfig_set(t *testing.T) {
	t.Parallel()

	t.Run("creates file when absent", func(t *testing.T) {
		t.Parallel()
		p := newDockerTestDir(t)

		assert.NilError(t, updateDockerConfig(p.configFile, func(cfg map[string]any) bool {
			cfg["currentContext"] = "rancher-desktop-2"
			return true
		}))

		data, err := os.ReadFile(p.configFile)
		assert.NilError(t, err)
		var cfg map[string]any
		assert.NilError(t, json.Unmarshal(data, &cfg))
		assert.Equal(t, cfg["currentContext"], "rancher-desktop-2")
	})

	t.Run("updates existing file preserving other keys", func(t *testing.T) {
		t.Parallel()
		p := newDockerTestDir(t)
		assert.NilError(t, os.WriteFile(p.configFile, []byte(`{"auths":{"example.com":{}}}`+"\n"), 0o600))

		assert.NilError(t, updateDockerConfig(p.configFile, func(cfg map[string]any) bool {
			cfg["currentContext"] = "rancher-desktop-2"
			return true
		}))

		data, err := os.ReadFile(p.configFile)
		assert.NilError(t, err)
		var cfg map[string]any
		assert.NilError(t, json.Unmarshal(data, &cfg))
		assert.Equal(t, cfg["currentContext"], "rancher-desktop-2")
		_, hasAuths := cfg["auths"]
		assert.Assert(t, hasAuths, "auths key must be preserved")
	})

	t.Run("no-op when mutate returns false", func(t *testing.T) {
		t.Parallel()
		p := newDockerTestDir(t)

		// File does not exist; mutate returns false → file must not be created.
		assert.NilError(t, updateDockerConfig(p.configFile, func(_ map[string]any) bool {
			return false
		}))
		_, err := os.Stat(p.configFile)
		assert.Assert(t, os.IsNotExist(err), "file should not have been created")
	})
}

func Test_updateDockerConfig_clear(t *testing.T) {
	t.Parallel()

	t.Run("no-op when file does not exist", func(t *testing.T) {
		t.Parallel()
		p := newDockerTestDir(t)

		assert.NilError(t, updateDockerConfig(p.configFile, func(cfg map[string]any) bool {
			if _, ok := cfg["currentContext"]; !ok {
				return false
			}
			delete(cfg, "currentContext")
			return true
		}))
		_, err := os.Stat(p.configFile)
		assert.Assert(t, os.IsNotExist(err), "file should not have been created")
	})

	t.Run("removes matching currentContext preserving other keys", func(t *testing.T) {
		t.Parallel()
		p := newDockerTestDir(t)
		assert.NilError(t, os.WriteFile(p.configFile,
			[]byte(`{"currentContext":"rancher-desktop-2","auths":{"example.com":{}}}`+"\n"), 0o600))

		assert.NilError(t, updateDockerConfig(p.configFile, func(cfg map[string]any) bool {
			if cfg["currentContext"] != "rancher-desktop-2" {
				return false
			}
			delete(cfg, "currentContext")
			return true
		}))

		data, err := os.ReadFile(p.configFile)
		assert.NilError(t, err)
		var cfg map[string]any
		assert.NilError(t, json.Unmarshal(data, &cfg))
		_, hasCtx := cfg["currentContext"]
		assert.Assert(t, !hasCtx, "currentContext should have been removed")
		_, hasAuths := cfg["auths"]
		assert.Assert(t, hasAuths, "auths key must be preserved")
	})
}

func Test_createDockerContext(t *testing.T) {
	p := newDockerTestDir(t)
	t.Setenv("HOME", filepath.Dir(p.configFile))

	assert.NilError(t, createReplaceDockerContext("rancher-desktop-2", "/tmp/docker.sock"))

	metaPath, err := contextMetaPath("rancher-desktop-2")
	assert.NilError(t, err)
	data, err := os.ReadFile(metaPath)
	assert.NilError(t, err)

	var meta dockerContextMeta
	assert.NilError(t, json.Unmarshal(data, &meta))
	assert.Equal(t, meta.Name, "rancher-desktop-2")
	assert.Equal(t, meta.Endpoints["docker"].Host, "unix:///tmp/docker.sock")
}

func Test_deleteDockerContext(t *testing.T) {
	p := newDockerTestDir(t)
	t.Setenv("HOME", filepath.Dir(p.configFile))

	assert.NilError(t, createReplaceDockerContext("rancher-desktop-2", "/tmp/docker.sock"))
	metaPath, err := contextMetaPath("rancher-desktop-2")
	assert.NilError(t, err)
	_, err = os.Stat(metaPath)
	assert.NilError(t, err, "meta.json should exist after create")

	assert.NilError(t, deleteDockerContext("rancher-desktop-2"))
	_, err = os.Stat(filepath.Dir(metaPath))
	assert.Assert(t, os.IsNotExist(err), "context dir should be removed")

	// Second delete is a no-op.
	assert.NilError(t, deleteDockerContext("rancher-desktop-2"))
}

func Test_currentDockerContext(t *testing.T) {
	p := newDockerTestDir(t)
	t.Setenv("HOME", filepath.Dir(p.configFile))

	t.Run("returns empty when file absent", func(t *testing.T) {
		name, err := getCurrentDockerContext()
		assert.NilError(t, err)
		assert.Equal(t, name, "")
	})

	t.Run("set then get", func(t *testing.T) {
		assert.NilError(t, setCurrentDockerContext("rancher-desktop-2"))
		name, err := getCurrentDockerContext()
		assert.NilError(t, err)
		assert.Equal(t, name, "rancher-desktop-2")
	})

	t.Run("clear only when context matches", func(t *testing.T) {
		assert.NilError(t, setCurrentDockerContext("rancher-desktop-2"))
		// Different name — should not clear.
		assert.NilError(t, clearCurrentDockerContext("rancher-desktop-3"))
		name, err := getCurrentDockerContext()
		assert.NilError(t, err)
		assert.Equal(t, name, "rancher-desktop-2")

		// Matching name — should clear.
		assert.NilError(t, clearCurrentDockerContext("rancher-desktop-2"))
		name, err = getCurrentDockerContext()
		assert.NilError(t, err)
		assert.Equal(t, name, "")
	})
}
