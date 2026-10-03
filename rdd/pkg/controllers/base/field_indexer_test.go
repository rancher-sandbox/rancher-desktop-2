// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package base

import (
	"reflect"
	"testing"

	"gotest.tools/v3/assert"
)

// TestAppendFieldValues_FlattensSlices verifies that a JSONPath result matching
// a slice- or map-of-slice-valued field is expanded into one index value per
// element, rather than being formatted as a single string representing the
// whole slice. A regression here silently breaks any `client.MatchingFields`
// lookup keyed on an individual element, since the indexed value would never
// match.
func TestAppendFieldValues_FlattensSlices(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected []string
	}{
		{
			name:     "scalar string",
			value:    "abc-123",
			expected: []string{"abc-123"},
		},
		{
			name:     "slice of strings",
			value:    []string{"abc-123", "def-456"},
			expected: []string{"abc-123", "def-456"},
		},
		{
			name:     "empty slice",
			value:    []string{},
			expected: nil,
		},
		{
			name:     "nested slice",
			value:    [][]string{{"a", "b"}, {"c"}},
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := appendFieldValues(nil, reflect.ValueOf(tc.value))
			assert.DeepEqual(t, got, tc.expected)
		})
	}
}

// TestAppendFieldValues_HandlesNilPointer verifies that a nil pointer or
// interface value (e.g. from a missing/absent field) is skipped rather than
// formatted as a literal "<nil>" index value.
func TestAppendFieldValues_HandlesNilPointer(t *testing.T) {
	var ptr *string
	got := appendFieldValues(nil, reflect.ValueOf(&ptr).Elem())
	assert.Assert(t, got == nil)
}

func TestAppendFieldValues_HandlesInvalidValue(t *testing.T) {
	got := appendFieldValues(nil, reflect.Value{})
	assert.Assert(t, got == nil)
}
