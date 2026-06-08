// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package socketbridge_test

import (
	"io"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/socketbridge"
)

// splitConn wraps separate read/write ends so the external test code and the
// Pipe function each own one side of the IO.  It satisfies HalfCloser.
//
// How the test is laid out (a and b are two separate, unrelated connections):
//
//	external→aIn  [pipe1]  a.Read   Pipe copies→  b.Write→bOut  [pipe3]  bOut→external
//	external→bIn  [pipe2]  b.Read   Pipe copies→  a.Write→aOut  [pipe4]  aOut→external
type splitConn struct {
	r         *io.PipeReader
	w         *io.PipeWriter
	writeDone bool
}

// splitConnPair bundles the two splitConns and their exposed pipe ends so
// callers avoid functions with more than five return values.
type splitConnPair struct {
	A, B *splitConn
	AIn  *io.PipeWriter
	BIn  *io.PipeWriter
	AOut *io.PipeReader
	BOut *io.PipeReader
}

func newSplitConnPair() splitConnPair {
	aInR, aInW := io.Pipe()
	bInR, bInW := io.Pipe()
	aOutR, aOutW := io.Pipe()
	bOutR, bOutW := io.Pipe()

	return splitConnPair{
		A:    &splitConn{r: aInR, w: aOutW},
		B:    &splitConn{r: bInR, w: bOutW},
		AIn:  aInW,
		BIn:  bInW,
		AOut: aOutR,
		BOut: bOutR,
	}
}

func (c *splitConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *splitConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *splitConn) Close() error {
	rerr := c.r.Close()
	werr := c.w.Close()
	if rerr != nil {
		return rerr
	}
	return werr
}

func (c *splitConn) CloseWrite() error {
	if c.writeDone {
		return nil
	}
	c.writeDone = true
	return c.w.Close()
}

func TestPipe_BidirectionalData(t *testing.T) {
	p := newSplitConnPair()

	pipeDone := make(chan error, 1)
	go func() { pipeDone <- socketbridge.Pipe(p.A, p.B) }()

	const fromA = "hello from A side"
	const fromB = "reply from B side"

	// Write to A's input; should emerge from B's output.
	go func() {
		if _, err := p.AIn.Write([]byte(fromA)); err != nil {
			t.Errorf("writing to AIn: %v", err)
		}
		p.AIn.Close()
	}()

	got := make([]byte, len(fromA))
	_, err := io.ReadFull(p.BOut, got)
	assert.NilError(t, err)
	assert.Equal(t, string(got), fromA)

	// Write to B's input; should emerge from A's output.
	go func() {
		if _, err := p.BIn.Write([]byte(fromB)); err != nil {
			t.Errorf("writing to BIn: %v", err)
		}
		p.BIn.Close()
	}()

	got2 := make([]byte, len(fromB))
	_, err = io.ReadFull(p.AOut, got2)
	assert.NilError(t, err)
	assert.Equal(t, string(got2), fromB)

	assert.NilError(t, <-pipeDone)
}

func TestPipe_HalfClose(t *testing.T) {
	// Verify Pipe completes cleanly when both input sides are closed without
	// writing any data; EOF propagates via CloseWrite to the other side.
	p := newSplitConnPair()

	pipeDone := make(chan error, 1)
	go func() { pipeDone <- socketbridge.Pipe(p.A, p.B) }()

	p.AIn.Close()
	p.BIn.Close()

	assert.NilError(t, <-pipeDone)
}
