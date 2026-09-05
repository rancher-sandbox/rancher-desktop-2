// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"strings"
	"testing"

	containerdclient "github.com/containerd/containerd/v2/client"
	"gotest.tools/v3/assert"

	"k8s.io/apimachinery/pkg/util/validation"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

func TestContainerdMirrorName(t *testing.T) {
	const hexID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("keeps a DNS1123 id as the mirror name", func(t *testing.T) {
		assert.Equal(t, containerdMirrorName("default", hexID), hexID)
	})

	t.Run("hashes an id that is not a valid object name", func(t *testing.T) {
		got := containerdMirrorName("default", "Has_Upper")
		assert.Assert(t, strings.HasPrefix(got, "ctr-"))
		assert.Equal(t, len(validation.IsDNS1123Subdomain(got)), 0)
	})

	t.Run("separates the namespace from the id", func(t *testing.T) {
		// Concatenated without a separator both of these are "abB_c", so this
		// fails if the separator is ever dropped from the hashed input.
		assert.Assert(t, containerdMirrorName("a", "bB_c") != containerdMirrorName("ab", "B_c"))
	})

	t.Run("is stable across calls", func(t *testing.T) {
		assert.Equal(t, containerdMirrorName("ns", "A_b"), containerdMirrorName("ns", "A_b"))
	})
}

func TestMapContainerdProcessStatus(t *testing.T) {
	tests := []struct {
		input containerdclient.ProcessStatus
		want  containersv1alpha1.ContainerStatusValue
	}{
		{containerdclient.Created, containersv1alpha1.ContainerStatusCreated},
		{containerdclient.Running, containersv1alpha1.ContainerStatusRunning},
		{containerdclient.Stopped, containersv1alpha1.ContainerStatusExited},
		{containerdclient.Paused, containersv1alpha1.ContainerStatusPaused},
		{containerdclient.Pausing, containersv1alpha1.ContainerStatusPausing},
		// A status containerd adds later must not fail CRD validation.
		{containerdclient.ProcessStatus("something-new"), containersv1alpha1.ContainerStatusUnknown},
	}
	for _, tc := range tests {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, mapContainerdProcessStatus(tc.input), tc.want)
		})
	}
}
