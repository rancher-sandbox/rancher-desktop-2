// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"runtime"
	"testing"

	"gotest.tools/v3/assert"
)

func TestMultiCallArgs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"rdd runs normally", []string{"rdd", "start"}, []string{"rdd", "start"}},
		{"rdd.exe runs normally", []string{"rdd.exe", "start"}, []string{"rdd.exe", "start"}},
		{"rdd with a multi-call subcommand", []string{"rdd", "kubectl", "cluster-info"}, []string{"rdd", "kubectl", "cluster-info"}},
		{"kubectl symlink", []string{"kubectl", "get", "pods"}, []string{"kubectl", "kubectl", "get", "pods"}},
		{"kubectl with path", []string{"/usr/local/bin/kubectl", "get"}, []string{"/usr/local/bin/kubectl", "kubectl", "get"}},
		{"kubectl.exe", []string{"kubectl.exe", "version"}, []string{"kubectl.exe", "kubectl", "version"}},
		{"kubectl no args", []string{"kubectl"}, []string{"kubectl", "kubectl"}},
		{"nerdctl symlink", []string{"nerdctl", "run", "-it"}, []string{"nerdctl", "nerdctl", "run", "-it"}},
		{"nerdctl.exe", []string{"nerdctl.exe", "images"}, []string{"nerdctl.exe", "nerdctl", "images"}},
		{"yq symlink", []string{"yq", ".foo"}, []string{"yq", "yq", ".foo"}},
		{"yq with extra extensions", []string{"yq.rdd.exe", ".foo"}, []string{"yq.rdd.exe", "yq", ".foo"}},
		{"name only contains kubectl", []string{"kubectllike", "get"}, []string{"kubectllike", "get"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.DeepEqual(t, multiCallArgs(tc.args), tc.want)
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
		{"rdd.exe selects no instance", "rdd.exe", ""},
		{"numeric instance", "rdd-3", "3"},
		{"instance with path", "/usr/local/bin/rdd-3", "3"},
		{"instance with extension", "rdd-9.exe", "9"},
		{"instance ends at the first dot", "rdd-1.2.exe", "1"},
		{"named instance", "rdd-vampire", "vampire"},
		{"uppercase prefix", "RDD-9.EXE", "9"},
		{"mixed-case prefix keeps the instance's case", "Rdd-Vampire", "Vampire"},
		{"empty instance", "rdd-", ""},
		{"empty instance with extension", "rdd-.exe", ""},
		{"other command", "kubectl", ""},
		{"name only contains rdd-", "xrdd-3", ""},
	}
	if runtime.GOOS == "windows" {
		// filepath.Base splits on backslashes only on Windows.
		cases = append(cases, testCase{"instance with backslash path", `C:\bin\rdd-9.exe`, "9"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, instanceFromName(tc.arg0), tc.want)
		})
	}
}
