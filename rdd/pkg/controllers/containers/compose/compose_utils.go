// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"crypto/sha256"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
)

// composeMirrorName returns the expected metadata.name for a ComposeProject
// or ComposeUpRequest object with the given namespace and name (i.e.
// status.namespace/status.name for ComposeProject, or spec.namespace/spec.name
// for ComposeUpRequest).
//
// The candidate name is namespace, followed by a dot, followed by the name.
// If that candidate is a valid Kubernetes name, it is used
// directly as metadata.name; otherwise, metadata.name is "cmp-" followed by
// the lower-case SHA-256 hash of the candidate name.
func composeMirrorName(namespace, name string) string {
	candidateName := namespace + "." + name
	if len(validation.IsDNS1123Subdomain(candidateName)) == 0 {
		return candidateName
	}
	return fmt.Sprintf("cmp-%x", sha256.Sum256([]byte(candidateName)))
}
