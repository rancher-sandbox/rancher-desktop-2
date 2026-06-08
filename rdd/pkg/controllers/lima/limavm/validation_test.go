// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package limavm

import (
	"context"
	"strings"
	"testing"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
)

func TestValidateTemplateData(t *testing.T) {
	testCases := []struct {
		name          string
		data          map[string]string
		expectError   bool
		errorContains string
	}{
		{
			name:          "missing template key",
			data:          map[string]string{"other": "value"},
			expectError:   true,
			errorContains: "must have a",
		},
		{
			name:          "empty template",
			data:          map[string]string{v1alpha1.TemplateConfigMapKey: ""},
			expectError:   true,
			errorContains: "cannot be empty",
		},
		{
			name:          "invalid YAML syntax",
			data:          map[string]string{v1alpha1.TemplateConfigMapKey: "invalid: yaml: {"},
			expectError:   true,
			errorContains: "failed to parse template",
		},
		{
			name:          "invalid Lima schema - bad arch",
			data:          map[string]string{v1alpha1.TemplateConfigMapKey: `arch: "invalid"`},
			expectError:   true,
			errorContains: "failed to validate template",
		},
		{
			name:        "valid minimal template",
			data:        map[string]string{v1alpha1.TemplateConfigMapKey: `images: [{location: "https://example.com/image.qcow2"}]`},
			expectError: false,
		},
		{
			name:        "valid template with memory",
			data:        map[string]string{v1alpha1.TemplateConfigMapKey: `{"memory":"2GB","images":[{"location":"https://example.com/image.qcow2"}]}`},
			expectError: false,
		},
		{
			// Currently Lima validation will only warn about unknown keys, but otherwise ignore them. rdd is disabling these warnings.
			name:        "valid template with unknown key",
			data:        map[string]string{v1alpha1.TemplateConfigMapKey: `{"unknown":true,"images":[{"location":"https://example.com/image.qcow2"}]}`},
			expectError: false,
		},
	}

	ctx := context.Background()
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateTemplateData(ctx, tc.data)

			if tc.expectError && err == nil {
				t.Errorf("Expected error, but got none")
			}
			if !tc.expectError && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			if tc.errorContains != "" && err != nil {
				if !strings.Contains(err.Error(), tc.errorContains) {
					t.Errorf("Expected error to contain %q, but got: %v", tc.errorContains, err)
				}
			}
		})
	}
}
