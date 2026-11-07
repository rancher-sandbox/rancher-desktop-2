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
	name := commandName(args[0])
	switch name {
	case "kubectl", "nerdctl", "yq":
		return append([]string{args[0], name}, args[1:]...)
	}
	return args
}

// instanceFromName returns the instance that an rdd-<instance> name selects,
// or "" when arg0 selects none. The rdd- prefix matches in any letter case, as
// file names do by default on Windows and macOS.
func instanceFromName(arg0 string) string {
	const prefix = "rdd-"
	name := commandName(arg0)
	if len(name) < len(prefix) || !strings.EqualFold(name[:len(prefix)], prefix) {
		return ""
	}
	return name[len(prefix):]
}

// commandName returns argv[0]'s basename cut at the first ".", so rdd-9.exe
// gives rdd-9.
func commandName(arg0 string) string {
	name, _, _ := strings.Cut(filepath.Base(arg0), ".")
	return name
}
