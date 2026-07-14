// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/hostinfo"

// HostInfo aliases the shared host-info type so the App webhooks can reference
// the detected host hardware limits unqualified. Detection lives in
// pkg/hostinfo so the webhooks and the HostInfo CRD reconciler share one
// computation and can never drift apart.
type HostInfo = hostinfo.HostInfo
