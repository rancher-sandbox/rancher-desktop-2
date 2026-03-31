// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package datastore

import (
	"context"

	"github.com/k3s-io/kine/pkg/endpoint"
)

// Config holds the kine datastore configuration.
type Config struct {
	EndpointConfig endpoint.Config
}

type completedConfig struct {
	*Config
}

// CompletedConfig wraps a finalized Config for passing to NewServer.
type CompletedConfig struct {
	*completedConfig
}

// NewConfig creates a Config from completed options.
func NewConfig(o CompletedOptions) (*Config, error) {
	return &Config{
		EndpointConfig: o.EndpointConfig,
	}, nil
}

// Complete the configuration and return a CompletedConfig for passing to NewServer.
func (c *Config) Complete() CompletedConfig {
	return CompletedConfig{&completedConfig{
		Config: c,
	}}
}

// Server wraps the kine embedded etcd server.
type Server struct {
	config CompletedConfig
}

// NewServer creates a Server from a CompletedConfig. Returns nil if the config is empty.
func NewServer(config CompletedConfig) *Server {
	if config.Config == nil {
		return nil
	}
	return &Server{
		config: config,
	}
}

// Run starts the embedded etcd server. endpoint.Listen starts the gRPC server
// in a background goroutine and returns once the listener is bound. Some gRPC
// "server preface" warnings from the kube-apiserver's etcd client are expected
// during the initial connection burst — these resolve via automatic retry and
// do not affect functionality.
func (s *Server) Run(ctx context.Context) error {
	_, err := endpoint.Listen(ctx, s.config.EndpointConfig)
	return err
}
