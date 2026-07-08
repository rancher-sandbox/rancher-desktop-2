// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"maps"
	"slices"
	"testing"

	"gotest.tools/v3/assert"
)

func Test_processK3sVersions(t *testing.T) {
	t.Run("versions", func(t *testing.T) {
		tests := []struct {
			name     string
			input    []string
			wantKeys []string
		}{
			{
				name:     "typical bundled format",
				input:    []string{"v1.32.0+k3s1", "v1.31.5+k3s1"},
				wantKeys: []string{"1.32.0", "1.31.5"},
			},
			{
				name:  "two k3s builds of same version normalize to one key",
				input: []string{"v1.32.0+k3s1", "v1.32.0+k3s2"},
				// Both strip to "1.32.0"; the set must contain exactly that key.
				wantKeys: []string{"1.32.0"},
			},
			{
				name:     "entry without v prefix",
				input:    []string{"1.30.0+k3s1"},
				wantKeys: []string{"1.30.0"},
			},
			{
				name:     "entry without + suffix",
				input:    []string{"v1.29.0"},
				wantKeys: []string{"1.29.0"},
			},
			{
				name:     "entry without v prefix or + suffix",
				input:    []string{"1.28.0"},
				wantKeys: []string{"1.28.0"},
			},
			{
				name:     "empty versions array",
				input:    []string{},
				wantKeys: []string{},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				input := k3sVersionsJSON{
					Versions: tt.input,
				}
				got := processK3sVersions(input)

				expectedKeys := slices.Sorted(slices.Values(tt.wantKeys))
				actualKeys := slices.Sorted(maps.Keys(got.Versions))

				assert.DeepEqual(t, actualKeys, expectedKeys)
			})
		}
	})
	t.Run("channels", func(t *testing.T) {
		tests := []struct {
			name  string
			input map[string]string
			want  map[string]string
		}{
			{
				name:  "aliases and minor-version channels",
				input: map[string]string{"stable": "1.34.6", "latest": "1.35.3", "v1.32": "1.32.13"},
				// parseK3sChannels strips the "v" prefix from the channel name, so "1.32" resolves too.
				want: map[string]string{"stable": "1.34.6", "latest": "1.35.3", "1.32": "1.32.13"},
			},
			{
				name:  "channel value normalizes to bare semver",
				input: map[string]string{"stable": "v1.34.6+k3s1"},
				want:  map[string]string{"stable": "1.34.6"},
			},
			{
				name:  "no channels",
				input: map[string]string{},
				want:  map[string]string{},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				input := k3sVersionsJSON{
					Channels: tt.input,
				}
				got := processK3sVersions(input)
				assert.DeepEqual(t, got.Channels, tt.want)
			})
		}
	})
}
