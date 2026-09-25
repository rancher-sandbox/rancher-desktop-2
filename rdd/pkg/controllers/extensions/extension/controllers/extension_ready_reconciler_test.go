// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

func TestExtensionReadyReconcilerReadyConditionFor(t *testing.T) {
	cases := []struct {
		name       string
		installed  *metav1.Condition
		started    *metav1.Condition
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{
			name:       "neither set",
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonCreated,
		},
		{
			name:       "installing",
			installed:  &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionInstalledReasonDownloading},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonInstalling,
		},
		{
			name:       "resolve failed is broken",
			installed:  &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionInstalledReasonResolveFailed},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonBroken,
		},
		{
			name:       "download failed is broken",
			installed:  &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionInstalledReasonDownloadFailed},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonBroken,
		},
		{
			name:       "installed, waiting to start",
			installed:  &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionInstalledReasonInstalled},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonStarting,
		},
		{
			name:       "installed and starting",
			installed:  &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionInstalledReasonInstalled},
			started:    &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionStartedReasonStarting},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonStarting,
		},
		{
			name:       "start failed is broken",
			installed:  &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionInstalledReasonInstalled},
			started:    &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionStartedReasonStartFailed},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonBroken,
		},
		{
			name:       "installed and started is ready",
			installed:  &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionInstalledReasonInstalled},
			started:    &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionStartedReasonStarted},
			wantStatus: metav1.ConditionTrue,
			wantReason: v1alpha1.ExtensionReadyReasonReady,
		},
		{
			name:       "stopping",
			installed:  &metav1.Condition{Status: metav1.ConditionTrue, Reason: v1alpha1.ExtensionInstalledReasonInstalled},
			started:    &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionStartedReasonStopping},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonStopping,
		},
		{
			name:       "uninstalling",
			installed:  &metav1.Condition{Status: metav1.ConditionFalse, Reason: v1alpha1.ExtensionInstalledReasonDeleting},
			wantStatus: metav1.ConditionFalse,
			wantReason: v1alpha1.ExtensionReadyReasonUninstalling,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, reason, message := readyConditionFor(c.installed, c.started)
			assert.Equal(t, status, c.wantStatus)
			assert.Equal(t, reason, c.wantReason)
			assert.Assert(t, message != "")
		})
	}
}
