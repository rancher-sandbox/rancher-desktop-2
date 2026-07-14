// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// TestPruneNilValues covers the spec map sent in an App create. A cleared
// property is a nil, which a merge patch turns into a JSON null to remove the
// field; a create has nothing to remove, and the CRD schema rejects a null for
// a typed field.
func TestPruneNilValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		spec map[string]any
		want map[string]any
	}{
		{
			name: "drops a cleared property",
			spec: map[string]any{"virtualMachine": map[string]any{"memory": nil}},
			want: map[string]any{},
		},
		{
			name: "keeps siblings of a cleared property",
			spec: map[string]any{"virtualMachine": map[string]any{"memory": nil, "cpus": int64(2)}},
			want: map[string]any{"virtualMachine": map[string]any{"cpus": int64(2)}},
		},
		{
			name: "keeps zero values that are not nil",
			spec: map[string]any{"running": false, "kubernetes": map[string]any{"version": ""}},
			want: map[string]any{"running": false, "kubernetes": map[string]any{"version": ""}},
		},
		{
			name: "leaves a spec without nils unchanged",
			spec: map[string]any{"virtualMachine": map[string]any{"cpus": int64(4)}},
			want: map[string]any{"virtualMachine": map[string]any{"cpus": int64(4)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Assert(t, cmp.DeepEqual(pruneNilValues(tt.spec), tt.want))
		})
	}
}

// TestPruneNilValuesLeavesSourceIntact pins that the create path does not strip
// the nil out of the caller's map: the same map becomes the merge patch, where
// the nil is what clears the property.
func TestPruneNilValuesLeavesSourceIntact(t *testing.T) {
	t.Parallel()

	spec := map[string]any{"virtualMachine": map[string]any{"memory": nil}}

	pruneNilValues(spec)

	vm, ok := spec["virtualMachine"].(map[string]any)
	assert.Assert(t, ok)
	memory, ok := vm["memory"]
	assert.Assert(t, ok, "the patch must keep the cleared property")
	assert.Assert(t, memory == nil)
}
