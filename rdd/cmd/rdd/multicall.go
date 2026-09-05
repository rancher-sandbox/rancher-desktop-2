// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: Copyright The Lima Authors

// The name detection here is adapted from MaybeRunYQ in
// https://github.com/lima-vm/lima/blob/master/cmd/yq/yq.go

package main

import (
	"path/filepath"
	"strings"
)

// multiCallArgs lets rdd run as an embedded command (kubectl, nerdctl, yq)
// when invoked under that name via a symlink or rename, inserting the
// matching subcommand for Cobra to dispatch. The name is argv[0]'s basename
// minus extensions; os.Executable() would resolve symlinks back to rdd on
// Linux.
func multiCallArgs(args []string) []string {
	name := filepath.Base(args[0])
	name, _, _ = strings.Cut(name, ".")
	switch name {
	case "kubectl", "nerdctl", "yq":
		return append([]string{args[0], name}, args[1:]...)
	}
	return args
}
