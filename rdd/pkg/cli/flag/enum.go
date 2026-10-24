// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package flag provides utilities for handling command-line flags, including
// enumerated types.
package flag

import (
	"fmt"
	"slices"
	"strings"
)

// EnumValue describes an enumeration for use with github.com/spf13/pflag.
type EnumValue struct {
	allowed []string // Allowed values
	val     string   // Current value
}

// NewEnumValue creates a new EnumValue with the specified allowed values; at
// least one value must be given.  The initial value is the first of the allowed
// values.
func NewEnumValue(allowed ...string) *EnumValue {
	if len(allowed) < 1 {
		panic("at least one allowed value must be provided")
	}
	return &EnumValue{
		allowed: allowed,
		val:     allowed[0],
	}
}

// String implements [github.com/spf13/pflag.Value].
func (v *EnumValue) String() string {
	return v.val
}

// Set implements [github.com/spf13/pflag.Value].
func (v *EnumValue) Set(newVal string) error {
	if slices.Contains(v.allowed, newVal) {
		v.val = newVal
		return nil
	}
	return fmt.Errorf("value %q is not one of the allowed values: %s", newVal, v.AllowedValues())
}

// Type implements [github.com/spf13/pflag.Value].
func (v *EnumValue) Type() string {
	return "enum"
}

// AllowedValues returns a string representation of the allowed values for this
// enum.
func (v *EnumValue) AllowedValues() string {
	return strings.Join(v.allowed, "|")
}
