// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
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
