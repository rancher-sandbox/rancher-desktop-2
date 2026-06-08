// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package socketbridge

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/go-logr/logr"
	"github.com/linuxkit/virtsock/pkg/hvsock"
)

const (
	// DockerPipeName is the well-known Windows named pipe that Docker CLI
	// and Docker Desktop use to reach dockerd.
	DockerPipeName = `\\.\pipe\docker_engine`

	// VsockForwardPort is the AF_VSOCK port that rdd-guest listens on inside
	// the Lima VM.  Must match the constant in cmd/rdd-guest/main.go.
	VsockForwardPort uint32 = 6660
)

// HostBridge listens on a Windows named pipe and forwards each accepted
// connection to the guest via Hyper-V vsock.
type HostBridge struct {
	pipeName  string
	vmGUID    hvsock.GUID
	vsockPort uint32
	log       logr.Logger
}

// NewDockerHostBridge creates a HostBridge that forwards the Docker named pipe
// to the guest vsock agent.
func NewDockerHostBridge(vmGUID hvsock.GUID, log logr.Logger) *HostBridge {
	return &HostBridge{
		pipeName:  DockerPipeName,
		vmGUID:    vmGUID,
		vsockPort: VsockForwardPort,
		log:       log.WithName("socket-bridge"),
	}
}

// Run listens for named-pipe connections and proxies them to the guest until
// ctx is cancelled.
func (b *HostBridge) Run(ctx context.Context) error {
	ln, err := winio.ListenPipe(b.pipeName, nil)
	if err != nil {
		return fmt.Errorf("socket-bridge: listen on %s: %w", b.pipeName, err)
	}
	b.log.Info("Listening", "pipe", b.pipeName, "vsockPort", b.vsockPort)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			return fmt.Errorf("socket-bridge: accept: %w", err)
		}
		go b.handleConn(conn)
	}
}

func (b *HostBridge) handleConn(pipeConn net.Conn) {
	defer pipeConn.Close()

	vsockConn, err := dialVsock(b.vmGUID, b.vsockPort)
	if err != nil {
		b.log.Error(err, "Failed to dial vsock", "port", b.vsockPort)
		return
	}

	// Attempt half-close path first; fall back to plain bidirectional copy if
	// either side does not implement CloseWrite (both named-pipe and vsock
	// connections normally do, but the net.Conn interface does not guarantee it).
	hc1, ok1 := pipeConn.(HalfCloser)
	hc2, ok2 := vsockConn.(HalfCloser)
	if ok1 && ok2 {
		if err := Pipe(hc1, hc2); err != nil {
			b.log.Error(err, "Pipe error")
		}
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := io.Copy(pipeConn, vsockConn); err != nil {
			b.log.Error(err, "Copy vsock→pipe error")
		}
		pipeConn.Close()
	}()
	if _, err := io.Copy(vsockConn, pipeConn); err != nil {
		b.log.Error(err, "Copy pipe→vsock error")
	}
	vsockConn.Close()
	<-done
}

// dialVsock dials a Hyper-V vsock connection to the given VM and port.
// Mirrors getVsockConnection in hostswitch_windows.go.
func dialVsock(vmGUID hvsock.GUID, port uint32) (net.Conn, error) {
	svcPort, err := hvsock.GUIDFromString(winio.VsockServiceID(port).String())
	if err != nil {
		return nil, fmt.Errorf("vsock service ID for port %d: %w", port, err)
	}
	return hvsock.Dial(hvsock.Addr{VMID: vmGUID, ServiceID: svcPort})
}
