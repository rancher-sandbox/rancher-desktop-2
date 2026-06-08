// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package controllers

import (
	"github.com/spf13/pflag"
)

// ControllersFlagUsage is the help text for the --controllers flag.
// Shared between the embedded service and any standalone binary that
// surfaces the same flag so the two cannot drift.
const ControllersFlagUsage = "Controllers to enable. Use '*' for all, " +
	"or a comma-separated list of controller or API-group names. " +
	"Groups: rdd, app, containers, lima. " +
	"Prefix a name with '-' to exclude it, e.g. '*,-demo' or 'containers,-engine'."

// Options holds the controller configuration options.
type Options struct {
	Controllers string // Controller selection specification (--controllers flag)
}

type completedOptions struct {
	Controllers string
}

// CompletedOptions holds the completed controller configuration options.
type CompletedOptions struct {
	*completedOptions
}

// AddFlags adds the flags for the controller options to the given FlagSet.
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	if o == nil {
		return
	}

	fs.StringVar(&o.Controllers, "controllers", "*", ControllersFlagUsage)
}

// Complete returns the completed configuration.
func (o *Options) Complete() CompletedOptions {
	return CompletedOptions{
		&completedOptions{
			Controllers: o.Controllers,
		},
	}
}

// Validate validates the options.
func (c *CompletedOptions) Validate() []error {
	return nil
}

// NewOptions creates a new Options instance.
func NewOptions() *Options {
	return &Options{}
}
