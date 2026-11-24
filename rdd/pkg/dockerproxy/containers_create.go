// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package dockerproxy

import (
	"fmt"
	"net/http"

	"github.com/go-logr/logr"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestexec"
)

// mountTypeNpipe is the HostConfig.Mounts type of a named pipe mount.
const mountTypeNpipe = "npipe"

// mungeContainersCreate rewrites the Windows host paths of the bind mounts in a
// POST /containers/create request to the paths where the WSL2 guest mounts
// them. It translates only drive-absolute paths. A relative path would resolve
// against rdd's working directory, not the client's, and a rooted path such as
// /var/run/docker.sock names a file in the guest. It replaces the body only if
// a path changed, so a request without a drive-absolute path reaches dockerd
// as the client sent it, unless reading the body fails. It never logs the
// body, whose Env can hold secrets.
func mungeContainersCreate(logger logr.Logger, req *http.Request) error {
	body, err := readContainersCreateBody(req)
	if err != nil {
		return err
	}

	modified := false
	for i, bind := range body.Binds {
		source, rest, found := guestexec.CutVolumeSource(bind)
		if !found || !guestexec.IsDriveAbsolute(source) {
			continue
		}
		translated, err := guestexec.TranslateHostPath(source)
		if err != nil {
			return fmt.Errorf("could not translate bind path %s: %w", source, err)
		}
		logger.V(1).Info("Translated bind path", "from", source, "to", translated)
		body.Binds[i] = translated + ":" + rest
		modified = true
	}

	for _, mount := range body.Mounts {
		if mount == nil {
			continue
		}
		if mount.Type == mountTypeNpipe {
			logger.Info("Named pipe mounts are not supported", "source", mount.Source)
		}
		if mount.Type != mountTypeBind || !guestexec.IsDriveAbsolute(mount.Source) {
			continue
		}
		translated, err := guestexec.TranslateHostPath(mount.Source)
		if err != nil {
			return fmt.Errorf("could not translate mount path %s: %w", mount.Source, err)
		}
		logger.V(1).Info("Translated mount path", "from", mount.Source, "to", translated)
		mount.Source = translated
		modified = true
	}

	if !modified {
		return nil
	}
	return body.writeTo(req)
}
