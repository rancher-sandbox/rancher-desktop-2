// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"fmt"
	"strings"

	"github.com/distribution/reference"
)

// SanitizeImageName derives the expected metadata.name for an Extension from
// its spec.image:
// - The `docker.io/library/` prefix is stripped if present
// - The registry is converted to lower case
// - The tag (if any) is stripped
// - Any occurrence of ':' (used for the registry port number) is replaced with '.'
// - All other characters are replaced with '-'
//
// For example, "registry.example.com:9876/rancher-sandbox/rancher-desktop/rdx-host-api-test"
// sanitizes to "registry.example.com.9876-rancher-sandbox-rancher-desktop-rdx-host-api-test".
func SanitizeImageName(image string) (string, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return "", fmt.Errorf("invalid image reference %q: %w", image, err)
	}
	// named.Name() already excludes any tag or digest.
	name := strings.TrimPrefix(named.Name(), "docker.io/library/")

	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r == ':', r == '.':
			b.WriteByte('.')
		default:
			b.WriteByte('-')
		}
	}
	return b.String(), nil
}
