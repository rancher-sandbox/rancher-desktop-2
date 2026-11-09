// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package main is the rdd-guest agent that runs inside the Lima/WSL2 VM.
// It forwards one vsock port each to the Docker and containerd sockets, so the
// Windows host can reach them over Hyper-V vsock.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mdlayher/vsock"
)

const (
	dockerVsockPort     = 6660
	dockerSockPath      = "/var/run/docker.sock"
	containerdVsockPort = 6661
	containerdSockPath  = "/run/containerd/containerd.sock"
)

// forwards maps each vsock port to the guest socket it serves.
var forwards = []struct {
	port     uint32
	sockPath string
}{
	{dockerVsockPort, dockerSockPath},
	{containerdVsockPort, containerdSockPath},
}

func main() {
	// Listen on every port before serving any, so a failed listen exits at start.
	listeners := make([]net.Listener, len(forwards))
	for i, f := range forwards {
		l, err := vsock.Listen(f.port, nil)
		if err != nil {
			log.Fatalf("vsock listen on port %d: %v", f.port, err)
		}
		listeners[i] = l
		log.Printf("rdd-guest: listening on vsock port %d for %s", f.port, f.sockPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	for i, l := range listeners {
		sockPath := forwards[i].sockPath
		wg.Go(func() { serve(ctx, l, sockPath) })
	}
	wg.Wait()
}

// serve accepts vsock connections on l and forwards each to sockPath until ctx
// is cancelled.
func serve(ctx context.Context, l net.Listener, sockPath string) {
	go func() {
		<-ctx.Done()
		if err := l.Close(); err != nil {
			log.Printf("rdd-guest: close listener for %s: %v", sockPath, err)
		}
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("rdd-guest: accept for %s: %v", sockPath, err)
			if errors.Is(err, syscall.ECONNABORTED) {
				continue
			}
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
				return
			}
			continue
		}
		go handleConn(ctx, conn, sockPath)
	}
}

// TODO: once rancher-desktop-daemon is public, replace the inlined halfCloser,
// pipe(), and handleConn() here with a direct import of pkg/socketbridge, which
// contains the implementations (HalfCloser interface, Pipe function).

// halfCloser is a net.Conn that can independently close the write side.
type halfCloser interface {
	net.Conn
	CloseWrite() error
}

// handleConn forwards bytes between the vsock connection and sockPath.
// It rejects connections that do not originate from the Windows host (CID 2 /
// vsock.Host): any process in any WSL2 distro shares the same vsock namespace
// and could otherwise gain root access to the engine API.
func handleConn(ctx context.Context, vsockConn net.Conn, sockPath string) {
	defer func() {
		if err := vsockConn.Close(); err != nil {
			log.Printf("rdd-guest: close vsock conn: %v", err)
		}
	}()

	addr, ok := vsockConn.RemoteAddr().(*vsock.Addr)
	if !ok || addr.ContextID != vsock.Host {
		log.Printf("rdd-guest: rejected connection from %v", vsockConn.RemoteAddr())
		return
	}

	sockConn, err := (&net.Dialer{}).DialContext(ctx, "unix", sockPath)
	if err != nil {
		log.Printf("rdd-guest: dial %s: %v", sockPath, err)
		return
	}
	defer func() {
		if err := sockConn.Close(); err != nil {
			log.Printf("rdd-guest: close %s conn: %v", sockPath, err)
		}
	}()

	vsockHC, ok := vsockConn.(halfCloser)
	if !ok {
		log.Printf("rdd-guest: vsock conn from %v does not support CloseWrite", vsockConn.RemoteAddr())
		return
	}
	sockHC, ok := sockConn.(halfCloser)
	if !ok {
		log.Printf("rdd-guest: %s conn for %v does not support CloseWrite", sockPath, vsockConn.RemoteAddr())
		return
	}
	pipe(vsockHC, sockHC)
}

// pipe bidirectionally proxies between a and b until both directions are done.
func pipe(a, b halfCloser) {
	var wg sync.WaitGroup

	forward := func(dst, src halfCloser) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err != nil && !errors.Is(err, io.EOF) {
			log.Printf("rdd-guest: copy: %v", err)
		}
		_ = dst.CloseWrite()
	}

	wg.Add(2)
	go forward(a, b)
	go forward(b, a)
	wg.Wait()
}
