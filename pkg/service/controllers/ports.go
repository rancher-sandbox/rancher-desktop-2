// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
)

// GetAvailablePort tries to use the desired port, but picks a random available port if it's not available.
// Returns the port number that was successfully bound.
func GetAvailablePort(ctx context.Context, desiredPort int) (int, error) {
	// First, try the desired port
	if desiredPort != 0 {
		if port, err := isPortAvailable(ctx, desiredPort); err == nil {
			return port, nil
		}
	}

	// If desired port is not available, let the system pick a random available port
	port, err := isPortAvailable(ctx, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to find available port: %w", err)
	}
	return port, nil
}

// isPortAvailable checks if a port is available by trying to bind to it on localhost,
// and returns the port bound.  The listener is closed before returning.  If it
// fails, returns zero.  The returned port is never zero on success.
func isPortAvailable(ctx context.Context, port int) (int, error) {
	address := "127.0.0.1:" + strconv.Itoa(port)
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	if addr, ok := listener.Addr().(*net.TCPAddr); ok && addr.Port != 0 {
		return addr.Port, nil
	}
	return 0, errors.New("failed to get listener port")
}
