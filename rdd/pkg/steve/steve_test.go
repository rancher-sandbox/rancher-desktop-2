// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package steve

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// failingListener fails every Accept with an error that net/http does not
// retry.
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) {
	return nil, errors.New("listener broke")
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	return listener
}

// serveResult runs serve and returns its error, failing the test if serve
// does not return.
func serveResult(ctx context.Context, t *testing.T, listener net.Listener) error {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- serve(ctx, listener, http.NotFoundHandler()) }()
	select {
	case err := <-errc:
		return err
	case <-time.After(5 * time.Second):
		assert.Assert(t, false, "serve did not return")
		return nil
	}
}

func TestServeReturnsWhenServingFails(t *testing.T) {
	err := serveResult(t.Context(), t, failingListener{listen(t)})
	assert.ErrorContains(t, err, "listener broke")
}

func TestServeReturnsNilWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.NilError(t, serveResult(ctx, t, listen(t)))
}
