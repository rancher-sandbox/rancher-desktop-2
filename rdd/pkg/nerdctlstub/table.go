// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// commandSpec describes one nerdctl command in the generated parse table
// (nerdctl_commands_generated.go, written by ./generate).
type commandSpec struct {
	// valueFlags are the options that consume a value, in both long and
	// shorthand spellings.
	valueFlags []string
	// boolFlags are the options that take no value argument.
	boolFlags []string
	// subcommands are the names of directly nested commands.
	subcommands []string
	// foreignFlags means arguments after the first positional argument
	// belong to the command run inside the container, so option parsing
	// stops there.
	foreignFlags bool
}
