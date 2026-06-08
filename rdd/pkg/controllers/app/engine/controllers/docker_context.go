// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/context/docker"
	"github.com/docker/cli/cli/context/store"
	dockerclient "github.com/moby/moby/client"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
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
	// in the docker/cli metrics + telemetry chain (otel/sdk/metric, go-metrics,
	// gorilla/mux, backoff/v5, morikuni/aec).
	cfg := store.NewConfig(
		nil,
		store.EndpointTypeGetter(docker.DockerEndpoint, func() any { return &docker.EndpointMeta{} }),
	)
	return store.New(filepath.Join(dir, "contexts"), cfg), nil
}

func createReplaceDockerContext(name, endpointURL string) error {
	s, err := newContextStore()
	if err != nil {
		return err
	}
	return s.CreateOrUpdate(store.Metadata{
		Name:     name,
		Metadata: map[string]any{"Description": "Rancher Desktop " + name},
		Endpoints: map[string]any{
			docker.DockerEndpoint: docker.EndpointMeta{Host: endpointURL},
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

// pingDocker creates a Docker client with opts and pings it within ctx.
func pingDocker(ctx context.Context, opts ...dockerclient.Opt) bool {
	cli, err := dockerclient.New(opts...)
	if err != nil {
		return false
	}
	defer cli.Close()
	_, err = cli.Ping(ctx, dockerclient.PingOptions{})
	return err == nil
}

// probeDockerContext pings the implicit default Docker endpoint
// (DOCKER_HOST or the platform default socket) within dockerContextProbeTimeout.
// Used when currentContext is "" or "default".
func probeDockerContext(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, dockerContextProbeTimeout)
	defer cancel()
	return pingDocker(probeCtx, dockerclient.FromEnv)
}

// probeNamedDockerContext pings the Docker endpoint for the named context
// within dockerContextProbeTimeout and returns true if the endpoint is healthy.
//
// When we cannot determine whether the context is healthy — because the store
// is unreadable, the endpoint metadata is missing or malformed, the scheme is
// not tcp/unix/npipe (e.g. SSH), or the TLS config cannot be loaded — we return true
// (assume healthy). This conservatism prevents RDD from clobbering a context
// that is in the middle of being edited or migrated.
func probeNamedDockerContext(ctx context.Context, name string) bool {
	log := logf.FromContext(ctx).WithName("docker-context").WithValues("context", name)

	s, err := newContextStore()
	if err != nil {
		log.Error(err, "Cannot open context store; assuming context healthy")
		return true
	}
	md, err := s.GetMetadata(name)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false
		}
		log.Error(err, "Cannot read context metadata; assuming context healthy")
		return true
	}
	epMeta, err := docker.EndpointFromContext(md)
	if err != nil {
		log.Error(err, "Cannot decode docker endpoint; assuming context healthy")
		return true
	}
	scheme, _, _ := strings.Cut(epMeta.Host, "://")
	switch scheme {
	case "unix", "tcp", "npipe":
	default:
		log.Info("Unprobed endpoint scheme; assuming context healthy", "scheme", scheme)
		return true
	}
	ep, err := docker.WithTLSData(s, name, epMeta)
	if err != nil {
		log.Error(err, "Cannot load TLS data; assuming context healthy")
		return true
	}
	opts, err := ep.ClientOpts()
	if err != nil {
		log.Error(err, "Cannot build client options; assuming context healthy")
		return true
	}
	probeCtx, cancel := context.WithTimeout(ctx, dockerContextProbeTimeout)
	defer cancel()
	return pingDocker(probeCtx, opts...)
}
