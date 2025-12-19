// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package containers registers all controllers related to container engines.
package containers

import (
	// Import controllers to register them.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/container"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/image"
)
