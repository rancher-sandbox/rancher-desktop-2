// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package containers is a side-effect aggregator that blank-imports the
// container-related controller packages (container, containernamespace, image,
// volume, engine) so their init functions register them with the controller framework.
package containers

import (
	// Import controllers to register them.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/engine"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/container"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/containernamespace"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/image"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers/volume"
)
