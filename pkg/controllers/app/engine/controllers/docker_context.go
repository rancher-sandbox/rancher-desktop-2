// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/context/docker"
	"github.com/docker/cli/cli/context/store"
	mobyclient "github.com/moby/moby/client"
)

// dockerContextProbeTimeout is the maximum time allowed to ping a Docker
// socket when checking the user's current context is healthy.
const dockerContextProbeTimeout = 3 * time.Second

// dockerConfigDir returns $DOCKER_CONFIG when it is set, otherwise ~/.docker.
// We compute this ourselves rather than calling config.Dir() because that
// caches its result via sync.Once, which defeats t.Setenv("HOME", …) in tests.
func dockerConfigDir() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docker"), nil
}

// newContextStore builds a store.Store rooted at ~/.docker/contexts and
// configured with Docker CLI's standard metadata + endpoint types, so the
// files it writes are interoperable with the docker CLI.
func newContextStore() (store.Store, error) {
	dir, err := dockerConfigDir()
	if err != nil {
		return nil, err
	}
	// Pass nil for the context-metadata TypeGetter; the store decodes a nil
	// type into a map[string]any, which is all we need since we never inspect
	// the Metadata field. This avoids importing cli/command, which would pull
	// in the docker/cli metrics + telemetry chain (otel/sdk/metric, etc.).
	cfg := store.NewConfig(
		nil,
		store.EndpointTypeGetter(docker.DockerEndpoint, func() any { return &docker.EndpointMeta{} }),
	)
	return store.New(filepath.Join(dir, "contexts"), cfg), nil
}

func createReplaceDockerContext(name, socketPath string) error {
	s, err := newContextStore()
	if err != nil {
		return err
	}
	return s.CreateOrUpdate(store.Metadata{
		Name:     name,
		Metadata: map[string]any{"Description": "Rancher Desktop " + name},
		Endpoints: map[string]any{
			docker.DockerEndpoint: docker.EndpointMeta{Host: "unix://" + socketPath},
		},
	})
}

func deleteDockerContext(name string) error {
	s, err := newContextStore()
	if err != nil {
		return err
	}
	return s.Remove(name)
}

// getDockerContextHost returns the full Docker host URL (e.g. "unix:///path/to/docker.sock"
// or "tcp://192.168.1.1:2376") for the named context's docker endpoint.
// Returns an empty string if the context does not exist or has no docker endpoint.
func getDockerContextHost(name string) (string, error) {
	s, err := newContextStore()
	if err != nil {
		return "", err
	}
	md, err := s.GetMetadata(name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	ep, err := docker.EndpointFromContext(md)
	if err != nil {
		// Missing or mistyped docker endpoint — treat as empty, not an error.
		// The configured EndpointTypeGetter normally prevents the mistyped case.
		return "", nil
	}
	return ep.Host, nil
}

// getCurrentDockerContext reads the currentContext field from ~/.docker/config.json.
// Returns an empty string if the file does not exist or no context is set.
// config.Load validates every "auths" entry; a malformed credential fails
// this call (and set/clearCurrentDockerContext) until the user repairs
// config.json.
func getCurrentDockerContext() (string, error) {
	dir, err := dockerConfigDir()
	if err != nil {
		return "", err
	}
	cf, err := config.Load(dir)
	if err != nil {
		return "", err
	}
	return cf.CurrentContext, nil
}

func setCurrentDockerContext(name string) error {
	dir, err := dockerConfigDir()
	if err != nil {
		return err
	}
	cf, err := config.Load(dir)
	if err != nil {
		return err
	}
	if cf.CurrentContext == name {
		return nil
	}
	cf.CurrentContext = name
	return cf.Save()
}

func clearCurrentDockerContext(name string) error {
	dir, err := dockerConfigDir()
	if err != nil {
		return err
	}
	cf, err := config.Load(dir)
	if err != nil {
		return err
	}
	if cf.CurrentContext != name {
		return nil
	}
	cf.CurrentContext = ""
	return cf.Save()
}

// probeDockerContext tries to ping the Docker daemon at the given host URL.
// It returns true if the daemon responds within dockerContextProbeTimeout.
func probeDockerContext(ctx context.Context, host string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, dockerContextProbeTimeout)
	defer cancel()
	cli, err := mobyclient.New(mobyclient.WithHost(host))
	if err != nil {
		return false
	}
	defer cli.Close()
	_, err = cli.Ping(probeCtx, mobyclient.PingOptions{})
	return err == nil
}
