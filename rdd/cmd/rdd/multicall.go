// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: Copyright The Lima Authors

// The name detection here is adapted from MaybeRunYQ in
// https://github.com/lima-vm/lima/blob/master/cmd/yq/yq.go

package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const instancePrefix = "rdd-"

// multiCallArgs lets rdd run as an embedded command (kubectl, nerdctl, yq)
// when invoked under that name via a symlink or rename, inserting the
// matching subcommand for Cobra to dispatch. It rejects any name other than
// rdd, rdd-<instance> with a valid instance, kubectl, nerdctl, yq and the
// names that isReleaseName accepts. Names compare case-sensitively. It reads
// the name from argv[0] (see commandName), because os.Executable() would
// resolve symlinks back to rdd on Linux.
func multiCallArgs(args []string) ([]string, error) {
	name := commandName(args[0], runtime.GOOS)
	switch name {
	case "kubectl", "nerdctl", "yq":
		return append([]string{args[0], name}, args[1:]...), nil
	case "rdd":
		return args, nil
	}
	if isReleaseName(name) {
		return args, nil
	}
	base := filepath.Base(args[0])
	if suffix, ok := strings.CutPrefix(name, instancePrefix); ok {
		if err := instance.ValidateSuffix(suffix); err != nil {
			return nil, fmt.Errorf("unsupported command name %q: %w", base, err)
		}
		return args, nil
	}
	return nil, fmt.Errorf("unsupported command name %q: use rdd, rdd-<instance>, kubectl, nerdctl or yq", base)
}

// isReleaseName reports whether name starts with "rdd." and a digit, like the
// file name rdd.<version>.<platform>.<arch> that releases give the standalone
// rdd (rddArtifactName in pkg/rancher-desktop/utils/releaseArtifacts.ts).
// Requiring the digit keeps other dotted names, such as rdd.old, an error.
func isReleaseName(name string) bool {
	rest, ok := strings.CutPrefix(name, "rdd.")
	return ok && rest != "" && rest[0] >= '0' && rest[0] <= '9'
}

// instanceFromName returns the instance that an rdd-<instance> name selects,
// or "" when arg0 selects none. multiCallArgs has already validated the
// instance.
func instanceFromName(arg0 string) string {
	suffix, ok := strings.CutPrefix(commandName(arg0, runtime.GOOS), instancePrefix)
	if !ok {
		return ""
	}
	return suffix
}

// commandName returns argv[0]'s basename. On Windows it also drops a trailing
// ".exe" in any letter case, so rdd-9.EXE gives rdd-9; no other extension is
// stripped.
func commandName(arg0, goos string) string {
	name := filepath.Base(arg0)
	if goos == "windows" {
		const ext = ".exe"
		if len(name) > len(ext) && strings.EqualFold(name[len(name)-len(ext):], ext) {
			name = name[:len(name)-len(ext)]
		}
	}
	return name
}
