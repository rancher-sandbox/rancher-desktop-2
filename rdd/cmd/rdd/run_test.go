// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func TestIsKubeCommand(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		{"kubectl", true},
		{"helm", true},
		{"/usr/local/bin/kubectl", true},
		{"kubectl.exe", true},
		{"helm.exe", true},
		{"docker", false},
		{"kubectllike", false},
		{"helmfile", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			assert.Equal(t, isKubeCommand(tc.command), tc.want)
		})
	}
}

func TestRunHelpSkipsAppStart(t *testing.T) {
	// A help flag must print usage without starting the App. DisableFlagParsing
	// otherwise routes it through ensureAppRunning, which creates and starts a
	// real service, so reaching that path here would have side effects; a
	// passing run therefore also proves the early return.
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			cmd := newRunCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{flag})

			assert.NilError(t, cmd.Execute())
			assert.Assert(t, strings.Contains(out.String(), "Run a command against this Rancher Desktop instance"))
		})
	}
}

func TestSetupRunEnv(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("DOCKER_HOST", "tcp://stale:2375")
	t.Setenv("DOCKER_CONTEXT", "stale")
	t.Setenv("KUBECONFIG", "/old/kubeconfig")

	assert.NilError(t, setupRunEnv())

	binDir := filepath.Join(instance.ShortDir(), "bin")
	assert.Equal(t, os.Getenv("PATH"), binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	assert.Equal(t, os.Getenv("DOCKER_CONTEXT"), instance.Name())
	assert.Equal(t, os.Getenv("KUBECONFIG"), instance.KubeConfig())

	// DOCKER_HOST must be cleared so DOCKER_CONTEXT takes effect.
	_, hasDockerHost := os.LookupEnv("DOCKER_HOST")
	assert.Assert(t, !hasDockerHost)
}
