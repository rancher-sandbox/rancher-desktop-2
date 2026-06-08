// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package version

import (
	"fmt"
	"os"
	"reflect"

	"github.com/spf13/pflag"
)

// VersionFlag provides a custom --version flag implementation that mimics the behavior
// of k8s.io/component-base/version/verflag package.
//
// CONSTRAINTS AND DESIGN DECISIONS:
//
// We cannot use k8s.io/component-base/version/verflag directly in the main RDD CLI because:
//
// 1. INCOMPATIBLE VERSION SOURCES: verflag expects version information to be set via
//    k8s.io/component-base/version package global variables, but RDD uses its own
//    pkg/version package with different build-time ldflags integration.
//
// 2. GLOBAL STATE CONFLICTS: verflag uses global package-level state that would require
//    duplicating version information or complex synchronization between version packages.
//
// 3. DIFFERENT CLI PATTERNS: verflag is designed for Kubernetes server components that
//    use cliflag.NamedFlagSets, while RDD's main CLI uses standard Cobra patterns.
//
// 4. VERSION STRING FORMAT: verflag outputs Kubernetes-style version strings, but RDD
//    needs custom formatting that matches its own versioning scheme.
//
// However, we DO use verflag in pkg/service/cmd/service.go for the 'rdd svc serve' command
// because:
// - That command follows Kubernetes server component patterns
// - It uses NamedFlagSets and other Kubernetes CLI conventions
// - It benefits from verflag's integration with Kubernetes logging and metrics
//
// This dual approach allows us to:
// - Use standard Cobra patterns for the main RDD CLI
// - Use Kubernetes patterns for the server component
// - Avoid version information duplication or synchronization issues

// Value implements pflag.Value for a custom --version flag that supports multiple formats.
//
// Supported formats:
//   - --version (no value): prints simple version and exits
//   - --version=raw: prints detailed version information and exits
//   - --version=vX.Y.Z: prints custom version string and exits
type Value struct {
	value string
}

// String returns the current value of the version flag.
func (v *Value) String() string {
	return v.value
}

// Set processes the version flag value and exits the program after printing version info.
// This matches the behavior of verflag.VersionValue.Set().
func (v *Value) Set(value string) error {
	v.value = value
	PrintAndExit(value)
	return nil
}

// Type returns the flag type name for help text.
func (v *Value) Type() string {
	return "version"
}

// PrintAndExit prints version information based on the provided format and exits.
//
// Formats:
//   - "raw": prints detailed version info (Version, GitCommit, BuildDate, etc.)
//   - "true" or "": prints simple version string
//   - any other value: prints that value as-is (allows version override)
func PrintAndExit(format string) {
	rddVersion := Get()

	switch format {
	case "raw":
		// Print detailed version info like kubectl does
		value := reflect.ValueOf(rddVersion)
		for i := range value.NumField() {
			fmt.Fprintf(os.Stdout, "%s: %s\n", value.Type().Field(i).Name, value.Field(i))
		}
	case "true", "":
		// Print simple version info
		fmt.Fprintln(os.Stdout, rddVersion.String())
	default:
		// Custom version override (like --version=v1.0.0)
		fmt.Fprintln(os.Stdout, format)
	}
	//nolint:revive // This is intentionally exiting when set.
	os.Exit(0)
}

// AddVersionFlag adds a --version flag to the provided FlagSet with verflag-compatible behavior.
//
// The flag supports:
//   - --version: prints version and exits
//   - --version=raw: prints detailed version and exits
//   - --version=vX.Y.Z: prints custom version and exits
//
// Returns the created flag for additional configuration if needed.
func AddVersionFlag(flags *pflag.FlagSet) *pflag.Flag {
	versionFlag := flags.VarPF(&Value{}, "version", "",
		"--version, --version=raw prints version information and quits; --version=vX.Y.Z... sets the reported version")
	versionFlag.NoOptDefVal = "true" // This allows --version without =value
	return versionFlag
}
