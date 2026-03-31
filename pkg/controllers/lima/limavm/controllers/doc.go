// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package controllers implements the LimaVM reconciler and hostagent watcher.
// The reconciler manages VM lifecycle state transitions; the watcher monitors
// each hostagent process and triggers reconciliation on state changes or
// crashes.
package controllers
