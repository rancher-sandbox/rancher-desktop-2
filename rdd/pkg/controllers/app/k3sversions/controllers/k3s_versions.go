// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers provides controllers to ensure a `k3s-versions` config
// map exists for the front end to display in preferences dialogs.
package controllers

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// k3sVersionsJSON mirrors the k3s-versions.json fields we consume.
type k3sVersionsJSON struct {
	Versions []string          `json:"versions"`
	Channels map[string]string `json:"channels"`
}

// K3sVersions describes the supported Kubernetes versions and channels.
type K3sVersions struct {
	// Versions is a mapping from a semver string (e.g. "1.32.4") to a k3s version
	// string (e.g. "v1.32.4+k3s1").
	Versions map[string]string
	// Channels is a mapping from a channel alias (e.g. "stable", "latest", "1.32")
	// to a concrete semver string (e.g. "1.32.4").
	Channels map[string]string
}

//go:embed k3s-versions.json
var k3sVersionsDataString string

// k3sVersions is the parsed k3s versions data.
var k3sVersions = sync.OnceValues(func() (K3sVersions, error) {
	var parsed k3sVersionsJSON
	if err := json.Unmarshal([]byte(k3sVersionsDataString), &parsed); err != nil {
		return K3sVersions{}, fmt.Errorf("failed to parse k3s versions: %w", err)
	}

	return processK3sVersions(parsed), nil
})

// k3sVersionData is the data that goes into the k3s-versions config map.
var k3sVersionData = sync.OnceValues(func() (map[string]string, error) {
	versions, err := k3sVersions()
	if err != nil {
		return nil, err
	}
	mapping := make(map[string]string)
	data, err := json.Marshal(versions.Versions)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal supported Kubernetes versions: %w", err)
	}
	mapping["versions"] = string(data)
	data, err = json.Marshal(versions.Channels)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal supported Kubernetes channels: %w", err)
	}
	mapping["channels"] = string(data)
	return mapping, nil
})

// bareVersion strips the leading "v" and "+k3s*" suffix, turning
// "v1.32.0+k3s1" into "1.32.0".
func bareVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	v, _, _ = strings.Cut(v, "+")
	return v
}

// processK3sVersions converts the raw k3s versions JSON structure into a form
// that is more useful for us.
func processK3sVersions(data k3sVersionsJSON) K3sVersions {
	result := K3sVersions{
		Versions: make(map[string]string, len(data.Versions)),
		Channels: make(map[string]string, len(data.Channels)),
	}
	// Sort the versions into some stable order; there should be no versions that
	// map to the same bare version, but this at least makes it consistent.
	for _, v := range slices.Sorted(slices.Values(data.Versions)) {
		result.Versions[bareVersion(v)] = v
	}
	for k, v := range data.Channels {
		result.Channels[strings.TrimPrefix(k, "v")] = bareVersion(v)
	}
	return result
}

// GetVersions returns the supported Kubernetes versions and channels.
func GetVersions() (K3sVersions, error) {
	return k3sVersions()
}
