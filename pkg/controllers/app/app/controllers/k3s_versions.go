// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// k3sVersionsJSON mirrors the k3s-versions.json versions.
type k3sVersionsJSON struct {
	Versions []string `json:"versions"`
}

// parseK3sVersions returns the supported Kubernetes versions from the
// embedded k3s-versions.json. The versions are stripped and stored as bare semver strings
// (e.g. "1.32.0") so they can be compared directly against App spec values.
func parseK3sVersions(data string) (map[string]struct{}, error) {
	var parsed k3sVersionsJSON
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse k3s versions: %w", err)
	}
	set := make(map[string]struct{}, len(parsed.Versions))
	for _, v := range parsed.Versions {
		// Strip leading "v" and "+k3s*" suffix: "v1.32.0+k3s1" → "1.32.0"
		bare := strings.TrimPrefix(v, "v")
		bare, _, _ = strings.Cut(bare, "+")
		set[bare] = struct{}{}
	}
	return set, nil
}
