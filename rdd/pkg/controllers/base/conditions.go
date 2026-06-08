// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionMessageMaxLen is the maximum number of runes allowed in a Kubernetes
// condition message. CRD validation enforces a 32768-character limit on this
// field; keeping messages within this bound avoids rejections from operations
// that produce very long error strings (e.g. accumulated retry errors from
// image downloads).
const ConditionMessageMaxLen = 32768

// TruncateConditionMessage ensures a condition message fits within
// ConditionMessageMaxLen runes. If the message is longer it is cut and a
// "… (truncated)" suffix is appended so observers know it is incomplete.
func TruncateConditionMessage(msg string) string {
	runes := []rune(msg)
	if len(runes) <= ConditionMessageMaxLen {
		return msg
	}
	const suffix = "… (truncated)"
	return string(runes[:ConditionMessageMaxLen-len([]rune(suffix))]) + suffix
}

// HasConditionWithReason reports whether conditions contains a condition
// of the given type with the given status and reason.
func HasConditionWithReason(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string) bool {
	c := apimeta.FindStatusCondition(conditions, conditionType)
	return c != nil && c.Status == status && c.Reason == reason
}
