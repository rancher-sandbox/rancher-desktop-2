// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"encoding/json"
	"fmt"
	"strings"
)

// k3sVersionsJSON mirrors the k3s-versions.json fields we consume.
type k3sVersionsJSON struct {
	Versions []string          `json:"versions"`
	Channels map[string]string `json:"channels"`
}

// bareVersion strips the leading "v" and "+k3s*" suffix, turning
// "v1.32.0+k3s1" into "1.32.0".
func bareVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	return v
}

// unmarshalK3sVersions parses the embedded k3s-versions.json into its Go form.
func unmarshalK3sVersions(data string) (k3sVersionsJSON, error) {
	var parsed k3sVersionsJSON
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return parsed, fmt.Errorf("failed to parse k3s versions: %w", err)
	}
	return parsed, nil
}

// parseK3sVersions returns the supported Kubernetes versions from the
// embedded k3s-versions.json. The versions are stored as bare semver strings
// (e.g. "1.32.0") so they can be compared directly against App spec values.
func parseK3sVersions(data string) (map[string]struct{}, error) {
	parsed, err := unmarshalK3sVersions(data)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(parsed.Versions))
	for _, v := range parsed.Versions {
		set[bareVersion(v)] = struct{}{}
	}
	return set, nil
}

// parseK3sChannels maps each channel alias from the embedded k3s-versions.json
// (e.g. "stable", "latest", "1.32") to its concrete bare version. It strips a
// leading "v" from each alias so that both "v1.32" and "1.32" resolve.
func parseK3sChannels(data string) (map[string]string, error) {
	parsed, err := unmarshalK3sVersions(data)
	if err != nil {
		return nil, err
	}
	channels := make(map[string]string, len(parsed.Channels))
	for name, version := range parsed.Channels {
		channels[strings.TrimPrefix(name, "v")] = bareVersion(version)
	}
	return channels, nil
}
