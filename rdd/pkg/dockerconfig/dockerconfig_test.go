// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package dockerconfig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"github.com/docker/cli/cli/config/configfile"
	"gotest.tools/v3/assert"
)

// newConfigTestDir points HOME and DOCKER_CONFIG at throwaway temp directories
// so Edit's lock and config.json land in isolation. Returns the config.json path.
func newConfigTestDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dockerDir := filepath.Join(home, ".docker")
	assert.NilError(t, os.MkdirAll(dockerDir, 0o700))
	t.Setenv("DOCKER_CONFIG", dockerDir)
	return filepath.Join(dockerDir, "config.json")
}

func readExtraDirs(t *testing.T, configFile string) []string {
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

func TestDirPrefersDockerConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Setenv("DOCKER_CONFIG", "")
	dir, err := Dir()
	assert.NilError(t, err)
	assert.Equal(t, dir, filepath.Join(home, ".docker"))

	override := t.TempDir()
	t.Setenv("DOCKER_CONFIG", override)
	dir, err = Dir()
	assert.NilError(t, err)
	assert.Equal(t, dir, override)
}

func TestEditSavesOnlyWhenChanged(t *testing.T) {
	configFile := newConfigTestDir(t)

	// changed=false must not create the file.
	err := Edit(context.Background(), func(*configfile.ConfigFile) (bool, error) {
		return false, nil
	})
	assert.NilError(t, err)
	_, statErr := os.Stat(configFile)
	assert.Assert(t, os.IsNotExist(statErr), "no save when unchanged")

	// changed=true persists the mutation.
	err = Edit(context.Background(), func(cf *configfile.ConfigFile) (bool, error) {
		cf.CLIPluginsExtraDirs = []string{"/a"}
		return true, nil
	})
	assert.NilError(t, err)
	assert.DeepEqual(t, readExtraDirs(t, configFile), []string{"/a"})
}

// TestEditSerializesConcurrentWriters proves the lock prevents a lost update:
// each goroutine appends a unique entry, and all survive. Without serialization,
// concurrent load/modify/save would drop entries.
func TestEditSerializesConcurrentWriters(t *testing.T) {
	configFile := newConfigTestDir(t)

	const writers = 8
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			entry := filepath.Join("/plugins", string(rune('a'+n)))
			err := Edit(context.Background(), func(cf *configfile.ConfigFile) (bool, error) {
				cf.CLIPluginsExtraDirs = append(cf.CLIPluginsExtraDirs, entry)
				return true, nil
			})
			assert.NilError(t, err)
		}(i)
	}
	wg.Wait()

	got := readExtraDirs(t, configFile)
	sort.Strings(got)
	want := make([]string, 0, writers)
	for i := range writers {
		want = append(want, filepath.Join("/plugins", string(rune('a'+i))))
	}
	sort.Strings(want)
	assert.DeepEqual(t, got, want)
}

// TestEditWrapsParseFailureAsMalformed checks that a config.json docker/cli
// cannot parse surfaces as ErrMalformed (permanent), while an I/O failure stays
// a plain error (repairable).
func TestEditWrapsParseFailureAsMalformed(t *testing.T) {
	noop := func(*configfile.ConfigFile) (bool, error) { return false, nil }

	t.Run("parse failure wraps ErrMalformed", func(t *testing.T) {
		configFile := newConfigTestDir(t)
		seed := []byte(`{"auths":{"example.com":{"auth":"not-base64"}}}` + "\n")
		assert.NilError(t, os.WriteFile(configFile, seed, 0o600))

		err := Edit(context.Background(), noop)
		assert.ErrorIs(t, err, ErrMalformed)
	})

	t.Run("I/O failure is not ErrMalformed", func(t *testing.T) {
		configFile := newConfigTestDir(t)
		// A directory where config.json should be makes config.Load fail with an
		// I/O error (*fs.PathError), not a parse error. Unlike chmod 0, reading a
		// directory also fails for root, so this holds in CI containers.
		assert.NilError(t, os.Mkdir(configFile, 0o755))

		err := Edit(context.Background(), noop)
		assert.Assert(t, err != nil)
		assert.Assert(t, !errors.Is(err, ErrMalformed), "an I/O failure must stay repairable")
	})
}
