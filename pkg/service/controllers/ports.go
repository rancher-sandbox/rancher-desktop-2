// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"net"
	"strconv"
)

// ResolvePort binds a TCP port on localhost to confirm availability. If the
// desired port is occupied, the OS assigns a random port instead. The listener
// is closed before returning so the caller can rebind it. Call this immediately
// before the port is needed to minimize the window between releasing and
// rebinding.
func ResolvePort(ctx context.Context, desired int) (int, error) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(desired))
	if err != nil {
		ln, err = lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return 0, fmt.Errorf("find available port (desired %d): %w", desired, err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port, nil
}
