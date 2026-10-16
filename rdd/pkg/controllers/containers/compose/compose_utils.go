// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// defaultReapDelay is the amount of time to wait before reaping an object after
// it has been marked for deletion.
const defaultReapDelay = 10 * time.Minute

// reapAnnotation is the annotation key used to allow overriding the reap delay.
// This is not supported; it is only used for testing.
const reapAnnotation = "containers.rancherdesktop.io/reap-after"

// mirrorFinalizer is added to mirror resources so user deletions
// are forwarded to the container engine before the resource is removed.
const mirrorFinalizer = "engine.rancherdesktop.io/mirror"

func getReapDelay(obj metav1.Object) time.Duration {
	if obj == nil {
		return defaultReapDelay
	}

	if val, ok := obj.GetAnnotations()[reapAnnotation]; ok {
		if dur, err := time.ParseDuration(val); err == nil {
			return dur
		}
	}

	return defaultReapDelay
}
