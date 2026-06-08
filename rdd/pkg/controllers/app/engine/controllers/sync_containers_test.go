// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestParseContainerName(t *testing.T) {
	tests := []struct {
		input         string
		wantNamespace string
		wantName      string
	}{
		{
			input:         "/plain",
			wantNamespace: containerNamespace,
			wantName:      "plain",
		},
		{
			input:         "plain",
			wantNamespace: containerNamespace,
			wantName:      "plain",
		},
		{
			input:         "/k8s.io/magical_gates",
			wantNamespace: "k8s.io",
			wantName:      "magical_gates",
		},
		{
			input:         "/ns/name/with/slashes",
			wantNamespace: "ns",
			wantName:      "name/with/slashes",
		},
		{
			input:         "",
			wantNamespace: containerNamespace,
			wantName:      "",
		},
		{
			input:         "/",
			wantNamespace: containerNamespace,
			wantName:      "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			ns, name := parseContainerName(tc.input)
			assert.DeepEqual(
				t,
				[]string{ns, name},
				[]string{tc.wantNamespace, tc.wantName},
			)
		})
	}
}
