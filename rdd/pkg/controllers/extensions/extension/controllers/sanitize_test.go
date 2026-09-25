// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSanitizeImageName(t *testing.T) {
	cases := [][]string{
		{
			"registry.example.com:9876/rancher-sandbox/rancher-desktop/rdx-host-api-test",
			"registry.example.com.9876-rancher-sandbox-rancher-desktop-rdx-host-api-test",
		},
		{
			"docker.io/library/hello-world:tag",
			"hello-world",
		},
		{
			"registry.example.com/image@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			"registry.example.com-image",
		},
		{
			"Invalid-Image", // Upper case is not allowed in image names.
			"",
		},
		{
			"Registry.Example.Com/image", // Upper case is allowed in registry names.
			"registry.example.com-image",
		},
	}

	for _, c := range cases {
		t.Run(c[0], func(t *testing.T) {
			actual, err := SanitizeImageName(c[0])
			if c[1] == "" {
				assert.ErrorContains(t, err, "invalid image reference", "unexpected result %q", actual)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, actual, c[1])
			}
		})
	}
}
