/*
Copyright © 2026 SUSE LLC
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
