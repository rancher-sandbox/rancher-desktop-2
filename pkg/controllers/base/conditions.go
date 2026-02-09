// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HasConditionWithReason reports whether conditions contains a condition
// of the given type with the given status and reason.
func HasConditionWithReason(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string) bool {
	c := apimeta.FindStatusCondition(conditions, conditionType)
	return c != nil && c.Status == status && c.Reason == reason
}
