// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	k3sversionscontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/k3sversions/controllers"
)

// testK3sVersions is a minimal k3s versions fixture for defaulter tests.
var testK3sVersions = k3sversionscontrollers.K3sVersions{
	Channels: map[string]string{
		"latest": "1.35.3",
		"stable": "1.34.6",
		"1.32":   "1.32.13",
	},
	Versions: map[string]string{
		"1.32.13": "v1.32.13+k3s1",
		"1.34.6":  "v1.34.6+k3s1",
		"1.35.3":  "v1.35.3+k3s1",
	},
}

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

	d := NewAppDefaulter(testK3sVersions, HostInfo{})

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

// Test_AppDefaulter_defaultsVMCPUs covers the cpu-count default the admission
// controller writes for spec.virtualMachine.cpus. It is not parallel because it
// sets RDD_VM_CPUS via t.Setenv.
func Test_AppDefaulter_defaultsVMCPUs(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		hostCPUs int
		specCPUs int
		wantCPUs int
		wantErr  string
	}{
		{
			name:     "unset cpus and no env uses the built-in default",
			wantCPUs: defaultVMCPUs,
		},
		{
			name:     "unset cpus takes the RDD_VM_CPUS override",
			env:      "3",
			wantCPUs: 3,
		},
		{
			name:     "explicit cpus wins over the env override",
			env:      "3",
			specCPUs: 4,
			wantCPUs: 4,
		},
		{
			name:     "built-in default is clamped to the host cpu count",
			hostCPUs: 1,
			wantCPUs: 1,
		},
		{
			name:     "env override is clamped to the host cpu count",
			env:      "8",
			hostCPUs: 4,
			wantCPUs: 4,
		},
		{
			name:    "non-numeric env value errors",
			env:     "many",
			wantErr: "invalid RDD_VM_CPUS",
		},
		{
			name:    "zero env errors",
			env:     "0",
			wantErr: "invalid RDD_VM_CPUS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(vmCPUsEnv, tt.env)

			d := NewAppDefaulter(testK3sVersions, HostInfo{CPUs: tt.hostCPUs})

			app := &v1alpha1.App{
				Spec: v1alpha1.AppSpec{
					VirtualMachine: v1alpha1.VirtualMachineSpec{CPUs: tt.specCPUs},
				},
			}
			err := d.Default(context.Background(), app)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, app.Spec.VirtualMachine.CPUs, tt.wantCPUs)
		})
	}
}

// Test_AppDefaulter_defaultsVMMemory covers the memory default the admission
// controller writes for spec.virtualMachine.memory: 25% of host memory, clamped
// to [minMemoryBytes, maxDefaultMemoryBytes].
func Test_AppDefaulter_defaultsVMMemory(t *testing.T) {
	t.Parallel()

	explicit := resource.NewQuantity(3*1024*1024*1024, resource.BinarySI)

	tests := []struct {
		name       string
		hostMemory int64
		specMemory *resource.Quantity
		wantBytes  int64
		wantErr    string
	}{
		{
			name:       "quarter of host memory when within bounds",
			hostMemory: 16 * 1024 * 1024 * 1024,
			wantBytes:  4 * 1024 * 1024 * 1024,
		},
		{
			name:       "capped at the 6 GiB maximum",
			hostMemory: 64 * 1024 * 1024 * 1024,
			wantBytes:  maxDefaultMemoryBytes,
		},
		{
			name:       "floored at the 2 GiB minimum on small hosts",
			hostMemory: 4 * 1024 * 1024 * 1024,
			wantBytes:  minMemoryBytes,
		},
		{
			name:       "floored at the minimum when host memory is unknown",
			hostMemory: 0,
			wantBytes:  minMemoryBytes,
		},
		{
			name:       "explicit memory is left untouched",
			hostMemory: 16 * 1024 * 1024 * 1024,
			specMemory: explicit,
			wantBytes:  explicit.Value(),
		},
		{
			name:       "errors when host memory is below the minimum",
			hostMemory: 1 * 1024 * 1024 * 1024,
			wantErr:    "below the",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := NewAppDefaulter(testK3sVersions, HostInfo{Memory: tt.hostMemory})

			app := &v1alpha1.App{
				Spec: v1alpha1.AppSpec{
					VirtualMachine: v1alpha1.VirtualMachineSpec{Memory: tt.specMemory},
				},
			}
			err := d.Default(context.Background(), app)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, app.Spec.VirtualMachine.Memory != nil)
			assert.Equal(t, app.Spec.VirtualMachine.Memory.Value(), tt.wantBytes)
		})
	}
}
