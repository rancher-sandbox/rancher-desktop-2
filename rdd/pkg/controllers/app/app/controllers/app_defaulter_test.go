// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// testK3sVersions is a minimal k3s-versions.json fixture for defaulter tests.
const testK3sVersions = `{
  "channels": {
    "latest": "1.35.3",
    "stable": "1.34.6",
    "v1.32": "1.32.13"
  },
  "versions": ["v1.32.13+k3s1", "v1.34.6+k3s1", "v1.35.3+k3s1"]
}`

func Test_AppDefaulter_Default(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enabled bool
		version string
		want    string
	}{
		{
			name:    "enabled without version defaults to stable",
			enabled: true,
			version: "",
			want:    "1.34.6",
		},
		{
			name:    "disabled without version stays empty",
			enabled: false,
			version: "",
			want:    "",
		},
		{
			name:    "stable resolves to its concrete version",
			enabled: true,
			version: "stable",
			want:    "1.34.6",
		},
		{
			name:    "latest resolves to its concrete version",
			enabled: true,
			version: "latest",
			want:    "1.35.3",
		},
		{
			name:    "v-prefixed minor channel resolves",
			enabled: true,
			version: "v1.32",
			want:    "1.32.13",
		},
		{
			name:    "bare minor channel resolves",
			enabled: true,
			version: "1.32",
			want:    "1.32.13",
		},
		{
			name:    "concrete version passes through unchanged",
			enabled: true,
			version: "1.34.6",
			want:    "1.34.6",
		},
		{
			name:    "unknown version passes through for the validator to reject",
			enabled: true,
			version: "9.9.9",
			want:    "9.9.9",
		},
		{
			name:    "channel resolves even when Kubernetes is disabled",
			enabled: false,
			version: "stable",
			want:    "1.34.6",
		},
	}

	d, err := NewAppDefaulter(testK3sVersions)
	assert.NilError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := &v1alpha1.App{
				Spec: v1alpha1.AppSpec{
					Kubernetes: v1alpha1.KubernetesSpec{
						Enabled: tt.enabled,
						Version: tt.version,
					},
				},
			}
			err := d.Default(context.Background(), app)
			assert.NilError(t, err)
			assert.Equal(t, app.Spec.Kubernetes.Version, tt.want)
		})
	}
}
