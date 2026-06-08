// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package notary

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
)

// ValidateNotaryValue validates a Notary value.
func ValidateNotaryValue(value string) error {
	// Validate that value doesn't start with "invalid" (case-insensitive)
	if strings.HasPrefix(strings.ToLower(value), "invalid") {
		return fmt.Errorf("spec.value cannot start with 'invalid' (case-insensitive): %q", value)
	}
	return nil
}

// ValidateNotary validates a complete Notary object and returns warnings.
func ValidateNotary(notary *v1alpha1.Notary) ([]string, error) {
	if notary == nil {
		return nil, errors.New("notary object cannot be nil")
	}

	var warnings []string

	// Perform validation that can fail
	if err := ValidateNotaryValue(notary.Spec.Value); err != nil {
		return warnings, err
	}

	// Add warnings for suboptimal but valid configurations
	if len(notary.Spec.Value) > 24 {
		warnings = append(warnings, fmt.Sprintf("spec.value is longer than 24 characters (%d chars) - consider using a shorter value for better readability", len(notary.Spec.Value)))
	}

	return warnings, nil
}
