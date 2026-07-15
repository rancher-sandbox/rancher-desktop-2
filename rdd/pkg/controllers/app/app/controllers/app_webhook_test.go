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
)

// testHostInfo is the synthetic host the validator tests bound against, so the
// boundaries hold regardless of the CPU count and memory of the machine running
// the suite.
var testHostInfo = HostInfo{CPUs: 4, Memory: 16 * 1024 * 1024 * 1024}

func quantity(bytes int64) *resource.Quantity {
	return resource.NewQuantity(bytes, resource.BinarySI)
}

func Test_AppValidator_validateVirtualMachine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hostInfo HostInfo
		vm       v1alpha1.VirtualMachineSpec
		wantErr  string
	}{
		{
			name:     "cpus and memory within the host limits",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 2, Memory: quantity(4 * 1024 * 1024 * 1024)},
		},
		{
			name:     "cpus exactly at the host count",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 4, Memory: quantity(minMemoryBytes)},
		},
		{
			name:     "cpus above the host count",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 5, Memory: quantity(minMemoryBytes)},
			wantErr:  "exceeds the host CPU count",
		},
		{
			name:     "negative cpus",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: -1, Memory: quantity(minMemoryBytes)},
			wantErr:  "must not be negative",
		},
		{
			// The mutating webhook fills in a concrete count first, so zero only
			// reaches the validator when that webhook is bypassed.
			name:     "zero cpus is left for the defaulter",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 0, Memory: quantity(minMemoryBytes)},
		},
		{
			name:     "memory exactly at the minimum",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 1, Memory: quantity(minMemoryBytes)},
		},
		{
			name:     "memory below the minimum",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 1, Memory: quantity(minMemoryBytes - 1)},
			wantErr:  "less than the minimum",
		},
		{
			name:     "memory exactly at the host total",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 1, Memory: quantity(testHostInfo.Memory)},
		},
		{
			name:     "memory above the host total",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 1, Memory: quantity(testHostInfo.Memory + 1)},
			wantErr:  "exceeds the host memory",
		},
		{
			name:     "unset memory is left for the defaulter",
			hostInfo: testHostInfo,
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 1},
		},
		{
			// A zero reading disables the ceiling rather than failing closed, so
			// an unsupported platform still admits every App.
			name:     "a zero host reading skips both ceilings",
			hostInfo: HostInfo{},
			vm:       v1alpha1.VirtualMachineSpec{CPUs: 999, Memory: quantity(999 * 1024 * 1024 * 1024)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v, err := NewAppValidator(testK3sVersions, tt.hostInfo)
			assert.NilError(t, err)

			err = v.validateVirtualMachine(tt.vm)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

// Test_AppValidator_rejectsOnCreateAndUpdate pins that both admission entry
// points enforce the VM limits. Controller-runtime dispatches them through
// separate methods, so covering only one leaves the other unguarded.
func Test_AppValidator_rejectsOnCreateAndUpdate(t *testing.T) {
	t.Parallel()

	v, err := NewAppValidator(testK3sVersions, testHostInfo)
	assert.NilError(t, err)

	app := &v1alpha1.App{
		Spec: v1alpha1.AppSpec{
			VirtualMachine: v1alpha1.VirtualMachineSpec{CPUs: testHostInfo.CPUs + 1},
		},
	}

	_, err = v.ValidateCreate(context.Background(), app)
	assert.ErrorContains(t, err, "exceeds the host CPU count")

	_, err = v.ValidateUpdate(context.Background(), &v1alpha1.App{}, app)
	assert.ErrorContains(t, err, "exceeds the host CPU count")
}
