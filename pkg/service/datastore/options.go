// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package datastore

import (
	"time"

	"github.com/k3s-io/kine/pkg/endpoint"
	etcdversion "go.etcd.io/etcd/api/v3/version"
)

// Options holds user-configurable datastore settings.
type Options struct {
	Enabled        bool
	EndpointConfig endpoint.Config
}

type completedOptions struct {
	Enabled        bool
	EndpointConfig endpoint.Config
}

// CompletedOptions wraps finalized Options for passing to NewConfig.
type CompletedOptions struct {
	*completedOptions
}

// NewOptions returns Options with default values.
func NewOptions() *Options {
	return &Options{
		Enabled: false,
		EndpointConfig: endpoint.Config{
			// TODO Setup certs
			CompactBatchSize:    1000,
			CompactInterval:     5 * time.Minute,
			CompactMinRetain:    1000,
			CompactTimeout:      5 * time.Second,
			EmulatedETCDVersion: etcdversion.Version,
			NotifyInterval:      5 * time.Second,
			PollBatchSize:       500,
		},
	}
}

// Complete the options and return a CompletedOptions for passing to NewConfig.
func (o Options) Complete() CompletedOptions {
	ret := CompletedOptions{
		&completedOptions{
			Enabled:        o.Enabled,
			EndpointConfig: o.EndpointConfig,
		},
	}
	return ret
}

// Validate validates the batteries options.
func (b CompletedOptions) Validate() []error {
	var errs []error
	return errs
}
