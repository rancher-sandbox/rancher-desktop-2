// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"runtime"
	"testing"

	"gotest.tools/v3/assert"
)

func TestCommandName(t *testing.T) {
	cases := []struct {
		arg0, goos, want string
	}{
		{"rdd-3.EXE", "windows", "rdd-3"},
		{"rdd-3.EXE", "darwin", "rdd-3.EXE"},
		{"/usr/local/bin/kubectl.exe", "windows", "kubectl"},
		{"/usr/local/bin/kubectl.exe", "darwin", "kubectl.exe"},
		{"rdd-1.2.exe", "windows", "rdd-1.2"},
		{"rdd-1.2.exe", "darwin", "rdd-1.2.exe"},
		{"rdd.Exe", "windows", "rdd"},
		{"rdd.2.0.0-alpha.3.windows.x86_64.exe", "windows", "rdd.2.0.0-alpha.3.windows.x86_64"},
		{".exe", "windows", ".exe"},
	}
	for _, tc := range cases {
		t.Run(tc.goos+" "+tc.arg0, func(t *testing.T) {
			assert.Equal(t, commandName(tc.arg0, tc.goos), tc.want)
		})
	}
}

func TestMultiCallArgs(t *testing.T) {
	type testCase struct {
		name    string
		args    []string
		want    []string
		wantErr string
	}
	cases := []testCase{
		{name: "rdd runs normally", args: []string{"rdd", "start"}, want: []string{"rdd", "start"}},
		{name: "rdd with a multi-call subcommand", args: []string{"rdd", "kubectl", "cluster-info"}, want: []string{"rdd", "kubectl", "cluster-info"}},
		{name: "rdd-<instance> runs normally", args: []string{"rdd-vampire", "start"}, want: []string{"rdd-vampire", "start"}},
		{name: "release file name runs normally", args: []string{"rdd.2.0.0-alpha.3.darwin.aarch64", "version"}, want: []string{"rdd.2.0.0-alpha.3.darwin.aarch64", "version"}},
		{name: "dotted name without a version", args: []string{"rdd.old"}, wantErr: `unsupported command name "rdd.old": use rdd`},
		{name: "rdd and a dot", args: []string{"rdd."}, wantErr: `unsupported command name "rdd.": use rdd`},
		{name: "kubectl symlink", args: []string{"kubectl", "get", "pods"}, want: []string{"kubectl", "kubectl", "get", "pods"}},
		{name: "kubectl with path", args: []string{"/usr/local/bin/kubectl", "get"}, want: []string{"/usr/local/bin/kubectl", "kubectl", "get"}},
		{name: "kubectl no args", args: []string{"kubectl"}, want: []string{"kubectl", "kubectl"}},
		{name: "nerdctl symlink", args: []string{"nerdctl", "run", "-it"}, want: []string{"nerdctl", "nerdctl", "run", "-it"}},
		{name: "steve symlink", args: []string{"steve", "--context", "foo"}, want: []string{"steve", "steve", "--context", "foo"}},
		{name: "yq symlink", args: []string{"yq", ".foo"}, want: []string{"yq", "yq", ".foo"}},
		{name: "invalid instance", args: []string{"rdd-1.2"}, wantErr: `unsupported command name "rdd-1.2": invalid instance name "1.2"`},
		{name: "empty instance", args: []string{"rdd-"}, wantErr: `invalid instance name ""`},
		{name: "unknown name", args: []string{"foo"}, wantErr: `unsupported command name "foo": use rdd`},
		{name: "uppercase rdd", args: []string{"RDD"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "uppercase prefix", args: []string{"RDD-3"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "mixed-case prefix", args: []string{"Rdd-Vampire"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "uppercase kubectl", args: []string{"KUBECTL"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "name only contains kubectl", args: []string{"kubectllike", "get"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "name only contains rdd-", args: []string{"xrdd-3"}, wantErr: "use rdd, rdd-<instance>"},
		{name: "debugger binary", args: []string{"__debug_bin123"}, wantErr: "use rdd, rdd-<instance>"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases,
			testCase{name: "rdd.exe runs normally", args: []string{"rdd.exe", "start"}, want: []string{"rdd.exe", "start"}},
			testCase{name: "release file name with .exe", args: []string{"rdd.2.0.0-alpha.3.windows.x86_64.exe", "version"}, want: []string{"rdd.2.0.0-alpha.3.windows.x86_64.exe", "version"}},
			testCase{name: "kubectl.exe", args: []string{"kubectl.exe", "version"}, want: []string{"kubectl.exe", "kubectl", "version"}},
			testCase{name: "steve.exe", args: []string{"steve.exe"}, want: []string{"steve.exe", "steve"}},
			testCase{name: "error keeps the extension", args: []string{"rdd-1.2.exe"}, wantErr: `unsupported command name "rdd-1.2.exe": invalid instance name "1.2"`},
		)
	} else {
		cases = append(cases,
			testCase{name: "rdd.exe is unknown", args: []string{"rdd.exe"}, wantErr: `unsupported command name "rdd.exe": use rdd`},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := multiCallArgs(tc.args)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestInstanceFromName(t *testing.T) {
	type testCase struct {
		name string
		arg0 string
		want string
	}
	cases := []testCase{
		{"rdd selects no instance", "rdd", ""},
		{"numeric instance", "rdd-3", "3"},
		{"instance with path", "/usr/local/bin/rdd-3", "3"},
		{"named instance", "rdd-vampire", "vampire"},
		{"uppercase prefix selects no instance", "RDD-9", ""},
		{"other command", "kubectl", ""},
		{"name only contains rdd-", "xrdd-3", ""},
	}
	if runtime.GOOS == "windows" {
		// filepath.Base splits on backslashes only on Windows.
		cases = append(cases,
			testCase{"instance with backslash path", `C:\bin\rdd-9.exe`, "9"},
			testCase{"instance with extension", "rdd-9.EXE", "9"},
		)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, instanceFromName(tc.arg0), tc.want)
		})
	}
}
