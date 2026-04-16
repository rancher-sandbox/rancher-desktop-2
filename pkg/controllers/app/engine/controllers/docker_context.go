// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	dockerclient "github.com/moby/moby/client"
)

// dockerContextProbeTimeout is the maximum time allowed to ping a Docker
// socket when checking the user's current context is healthy.
const dockerContextProbeTimeout = 3 * time.Second

// dockerContextMeta is the on-disk format of a Docker CLI context metadata file.
type dockerContextMeta struct {
	Name      string                        `json:"Name"`
	Metadata  map[string]any                `json:"Metadata"`
	Endpoints map[string]dockerEndpointMeta `json:"Endpoints"`
}

type dockerEndpointMeta struct {
	Host          string `json:"Host"`
	SkipTLSVerify bool   `json:"SkipTLSVerify"`
}

func dockerConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docker"), nil
}

// contextMetaPath returns the path to the meta.json file for the named context.
// Docker creates the directory name using sha256(contextName).
func contextMetaPath(name string) (string, error) {
	dir, err := dockerConfigDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(dir, "contexts", "meta", hex.EncodeToString(sum[:]), "meta.json"), nil
}

func createReplaceDockerContext(name, socketPath string) error {
	path, err := contextMetaPath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	meta := dockerContextMeta{
		Name:     name,
		Metadata: map[string]any{},
		Endpoints: map[string]dockerEndpointMeta{
			"docker": {Host: "unix://" + socketPath},
		},
	}
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func deleteDockerContext(name string) error {
	path, err := contextMetaPath(name)
	if err != nil {
		return err
	}
	err = os.RemoveAll(filepath.Dir(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// getDockerContextHost returns the full Docker host URL (e.g. "unix:///path/to/docker.sock"
// or "tcp://192.168.1.1:2376") for the named context's docker endpoint.
// Returns an empty string if the context does not exist or has no docker endpoint.
func getDockerContextHost(name string) (string, error) {
	path, err := contextMetaPath(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var meta dockerContextMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return "", err
	}
	return meta.Endpoints["docker"].Host, nil
}

// getCurrentDockerContext reads the currentContext field from ~/.docker/config.json.
// Returns an empty string if the file does not exist or no context is set.
func getCurrentDockerContext() (string, error) {
	dir, err := dockerConfigDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	name, _ := cfg["currentContext"].(string)
	return name, nil
}

func setCurrentDockerContext(name string) error {
	dir, err := dockerConfigDir()
	if err != nil {
		return err
	}
	return updateDockerConfig(filepath.Join(dir, "config.json"), func(cfg map[string]any) bool {
		if cfg["currentContext"] == name {
			return false
		}
		cfg["currentContext"] = name
		return true
	})
}

func clearCurrentDockerContext(name string) error {
	dir, err := dockerConfigDir()
	if err != nil {
		return err
	}
	return updateDockerConfig(filepath.Join(dir, "config.json"), func(cfg map[string]any) bool {
		if cfg["currentContext"] != name {
			return false
		}
		delete(cfg, "currentContext")
		return true
	})
}

// probeDockerContext tries to ping the Docker daemon at the given host URL.
// It returns true if the daemon responds within dockerContextProbeTimeout.
func probeDockerContext(ctx context.Context, host string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, dockerContextProbeTimeout)
	defer cancel()
	cli, err := dockerclient.New(dockerclient.WithHost(host))
	if err != nil {
		return false
	}
	defer cli.Close()
	_, err = cli.Ping(probeCtx, dockerclient.PingOptions{})
	return err == nil
}

// updateDockerConfig reads the Docker config file at path, applies mutate,
// and writes back atomically via a temp-file rename. All other keys are
// preserved. mutate returns true if it changed cfg; if it returns false the
// function is a no-op and no I/O is performed. If the file does not exist and
// mutate returns true, the file is created.
func updateDockerConfig(path string, mutate func(map[string]any) bool) error {
	cfg := map[string]any{}
	data, err := os.ReadFile(path)
	notExist := errors.Is(err, os.ErrNotExist)
	switch {
	case err == nil:
		if jsonErr := json.Unmarshal(data, &cfg); jsonErr != nil {
			return jsonErr
		}
	case notExist:
		// file will be created below if mutate signals a change
	default:
		return err
	}

	if changed := mutate(cfg); !changed {
		return nil
	}

	out, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config.json.*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(out, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
