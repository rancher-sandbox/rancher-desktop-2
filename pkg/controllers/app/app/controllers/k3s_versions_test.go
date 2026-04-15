// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"
)

func Test_parseK3sVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantKeys []string
		wantErr  bool
	}{
		{
			name:     "typical bundled format",
			input:    `{"versions":["v1.32.0+k3s1","v1.31.5+k3s1"]}`,
			wantKeys: []string{"1.32.0", "1.31.5"},
		},
		{
			name:  "two k3s builds of same version normalize to one key",
			input: `{"versions":["v1.32.0+k3s1","v1.32.0+k3s2"]}`,
			// Both strip to "1.32.0"; the set must contain exactly that key.
			wantKeys: []string{"1.32.0"},
		},
		{
			name:     "entry without v prefix",
			input:    `{"versions":["1.30.0+k3s1"]}`,
			wantKeys: []string{"1.30.0"},
		},
		{
			name:     "entry without + suffix",
			input:    `{"versions":["v1.29.0"]}`,
			wantKeys: []string{"1.29.0"},
		},
		{
			name:     "entry without v prefix or + suffix",
			input:    `{"versions":["1.28.0"]}`,
			wantKeys: []string{"1.28.0"},
		},
		{
			name:     "empty versions array",
			input:    `{"versions":[]}`,
			wantKeys: []string{},
		},
		{
			name:    "malformed JSON",
			input:   `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseK3sVersions(tt.input)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an error but got nil")
				return
			}
			assert.NilError(t, err)

			for _, key := range tt.wantKeys {
				_, ok := got[key]
				assert.Assert(t, ok, "expected key %q not found in result %v", key, got)
			}
			assert.Equal(t, len(got), len(tt.wantKeys), "result has wrong number of keys: %v", got)
		})
	}
}
