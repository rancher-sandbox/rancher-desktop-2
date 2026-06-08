// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package socketbridge provides a raw bidirectional byte proxy used to forward
// a host-side socket to a guest-side socket across a transport boundary (e.g.
// AF_VSOCK on Windows/Hyper-V).
package socketbridge

import (
	"errors"
	"io"
	"sync"
)

// HalfCloser is implemented by connections that support half-close semantics
// (e.g. net.TCPConn, named-pipe connections, vsock connections).  When one
// direction reaches EOF the write side of the peer connection is closed so the
// remote end receives a clean EOF rather than waiting forever.
type HalfCloser interface {
	io.ReadWriteCloser
	// CloseWrite shuts down the write side of the connection without closing
	// the read side.
	CloseWrite() error
}

// Pipe copies data bidirectionally between c1 and c2 until both directions are
// done.  When a read from one side returns io.EOF (or any error), the write
// side of the other connection is half-closed so the remote peer gets a clean
// EOF.  Pipe blocks until both copy goroutines have exited and then closes
// both connections.
func Pipe(c1, c2 HalfCloser) error {
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	record := func(err error) {
		if err != nil && !errors.Is(err, io.EOF) {
			mu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			mu.Unlock()
		}
	}

	forward := func(dst, src HalfCloser) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		record(err)
		// Half-close the write side of dst so the remote peer sees EOF.
		record(dst.CloseWrite())
	}

	wg.Add(2)
	go forward(c1, c2)
	go forward(c2, c1)
	wg.Wait()

	c1.Close()
	c2.Close()
	return firstErr
}
