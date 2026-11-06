// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kube

import (
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStatusDebugStringFormatsDebugErrorArgs(t *testing.T) {
	err := &apierrors.StatusError{
		ErrStatus: v1.Status{
			Status:  v1.StatusFailure,
			Message: "test status message",
			Reason:  v1.StatusReasonForbidden,
			Code:    403,
		},
	}

	actual := statusDebugString(err)

	if strings.Contains(actual, "%!") {
		t.Fatalf("status debug string contains fmt error: %s", actual)
	}
	if !strings.Contains(actual, "test status message") {
		t.Fatalf("status debug string did not include status message: %s", actual)
	}
}
