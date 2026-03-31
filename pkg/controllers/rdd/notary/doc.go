// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package notary registers the Notary controller. A Notary watches a ConfigMap
// key and records each observed value change into a separate ConfigMap,
// providing an audit trail for configuration changes.
package notary
