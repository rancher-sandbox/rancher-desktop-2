// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package notary

import (
	"strings"
	"testing"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
)

func TestValidateNotaryValue(t *testing.T) {
	testCases := []struct {
		name        string
		value       string
		expectError bool
	}{
		{
			name:        "valid short value",
			value:       "valid-value",
			expectError: false,
		},
		{
			name:        "valid long value",
			value:       "this-is-a-very-long-value-that-is-still-valid",
			expectError: false,
		},
		{
			name:        "empty value",
			value:       "",
			expectError: false,
		},
		{
			name:        "invalid lowercase",
			value:       "invalid-value",
			expectError: true,
		},
		{
			name:        "invalid uppercase",
			value:       "INVALID-VALUE",
			expectError: true,
		},
		{
			name:        "invalid mixed case",
			value:       "Invalid-Value",
			expectError: true,
		},
		{
			name:        "contains invalid but doesn't start with it",
			value:       "not-invalid-but-contains-it",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateNotaryValue(tc.value)
			if tc.expectError && err == nil {
				t.Errorf("Expected error for value %q, but got none", tc.value)
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error for value %q, but got: %v", tc.value, err)
			}
		})
	}
}

func TestValidateNotary(t *testing.T) {
	testCases := []struct {
		name            string
		notary          *v1alpha1.Notary
		expectError     bool
		expectWarnings  int
		warningContains string
	}{
		{
			name: "valid short value - no warnings",
			notary: &v1alpha1.Notary{
				Spec: v1alpha1.NotarySpec{
					Value: "short-value",
				},
			},
			expectError:    false,
			expectWarnings: 0,
		},
		{
			name: "valid long value - should warn",
			notary: &v1alpha1.Notary{
				Spec: v1alpha1.NotarySpec{
					Value: "this-is-a-very-long-value-that-exceeds-24-characters",
				},
			},
			expectError:     false,
			expectWarnings:  1,
			warningContains: "longer than 24 characters",
		},
		{
			name: "exactly 24 characters - no warning",
			notary: &v1alpha1.Notary{
				Spec: v1alpha1.NotarySpec{
					Value: "exactly24characters-long", // exactly 24 chars
				},
			},
			expectError:    false,
			expectWarnings: 0,
		},
		{
			name: "25 characters - should warn",
			notary: &v1alpha1.Notary{
				Spec: v1alpha1.NotarySpec{
					Value: "exactly25characters-long!", // exactly 25 chars
				},
			},
			expectError:     false,
			expectWarnings:  1,
			warningContains: "longer than 24 characters (25 chars)",
		},
		{
			name: "invalid value - should error (no warnings)",
			notary: &v1alpha1.Notary{
				Spec: v1alpha1.NotarySpec{
					Value: "invalid-this-should-fail",
				},
			},
			expectError:    true,
			expectWarnings: 0, // Warnings returned with error, but we expect error to be primary concern
		},
		{
			name:           "nil notary - should error",
			notary:         nil,
			expectError:    true,
			expectWarnings: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := ValidateNotary(tc.notary)

			if tc.expectError && err == nil {
				t.Errorf("Expected error, but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}

			if len(warnings) != tc.expectWarnings {
				t.Errorf("Expected %d warnings, but got %d: %v", tc.expectWarnings, len(warnings), warnings)
			}

			if tc.warningContains != "" && len(warnings) > 0 {
				found := false
				for _, warning := range warnings {
					if strings.Contains(warning, tc.warningContains) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected warning to contain %q, but warnings were: %v", tc.warningContains, warnings)
				}
			}
		})
	}
}
