// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package api provides utility functions for working with the RDD API.
package api

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// MirrorName returns the mirrored name for a given container resource.  The
// prefix is prepended to the mirrored name with a dash if the input is not a
// valid Kubernetes object name.
func MirrorName(prefix, input string) string {
	needsEscape := len(validation.IsDNS1123Subdomain(input)) > 0
	if !needsEscape {
		after, found := strings.CutPrefix(input, prefix+"-")
		if found && len(after) == 64 {
			needsEscape = strings.TrimLeft(after, "0123456789abcdef") == ""
		}
	}
	if !needsEscape {
		return input
	}
	return fmt.Sprintf("%s-%x", prefix, sha256.Sum256([]byte(input)))
}
