// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package dockerproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"gotest.tools/v3/assert"
)

// testTimeout bounds every blocking step, so that a bug fails the test
// instead of hanging it.
const testTimeout = 10 * time.Second

// startProxy runs Serve on a loopback listener, with backendAddr as the
// backend, and returns the proxy's address. When the test ends, it stops Serve
// and checks that Serve returned nil.
func startProxy(t *testing.T, backendAddr string) string {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	dial := func(ctx context.Context) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", backendAddr)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, logr.Discard(), listener, dial) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NilError(t, err)
		case <-time.After(testTimeout):
			assert.Check(t, false, "Serve did not return after its context ended")
		}
	})
	return listener.Addr().String()
}

// startBackend serves handler as a fake dockerd on a loopback listener and
// returns the listener's address. The server stops when the test ends.
func startBackend(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	backend := listenBackend(t)
	server := &http.Server{ReadHeaderTimeout: testTimeout, Handler: handler}
	go func() { _ = server.Serve(backend) }()
	t.Cleanup(func() { server.Close() })
	return backend.Addr().String()
}

// listenBackend returns a loopback listener for a fake dockerd, closed when
// the test ends.
func listenBackend(t *testing.T) net.Listener {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	assert.NilError(t, err)
	t.Cleanup(func() { listener.Close() })
	return listener
}

func TestServeTranslatesContainersCreate(t *testing.T) {
	type received struct {
		requestURI string
		body       []byte
	}
	requests := make(chan received, 1)
	backendAddr := startBackend(t, func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		requests <- received{requestURI: req.RequestURI, body: body}
		w.Header().Set("Api-Version", "1.47")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"abc","Warnings":[]}`)
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	input := `{"Image":"alpine","Unknown":{"a":1},"HostConfig":{"Binds":["C:\\src:/src:ro"]}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+proxyAddr+"/v1.47/containers/create?name=x", strings.NewReader(input))
	assert.NilError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusCreated)
	assert.Equal(t, resp.Header.Get("Api-Version"), "1.47")
	assert.Equal(t, string(respBody), `{"Id":"abc","Warnings":[]}`)

	var got received
	select {
	case got = <-requests:
	case <-ctx.Done():
		assert.Assert(t, false, "the backend did not receive the request")
	}
	assert.Equal(t, got.requestURI, "/v1.47/containers/create?name=x")
	var body struct {
		Unknown    map[string]int
		HostConfig struct{ Binds []string }
	}
	assert.NilError(t, json.Unmarshal(got.body, &body))
	assert.DeepEqual(t, body.HostConfig.Binds, []string{"/mnt/c/src:/src:ro"})
	assert.DeepEqual(t, body.Unknown, map[string]int{"a": 1})
}

func TestServeTranslatesOnlyContainersCreate(t *testing.T) {
	bodies := make(chan string, 1)
	backendAddr := startBackend(t, func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		bodies <- string(body)
		w.WriteHeader(http.StatusNoContent)
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	// The proxy reads and translates only container create bodies. Others,
	// such as a `docker load` tarball, stream through to dockerd, so the
	// bind in this body must reach the backend as the client sent it.
	input := `{"HostConfig":{"Binds":["C:\\src:/src"]}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+proxyAddr+"/v1.47/containers/abc/start", strings.NewReader(input))
	assert.NilError(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusNoContent)
	select {
	case body := <-bodies:
		assert.Equal(t, body, input)
	case <-ctx.Done():
		assert.Assert(t, false, "the backend did not receive the request")
	}
}

func TestServeForwardsEmptyContainersCreate(t *testing.T) {
	backendAddr := startBackend(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "config cannot be empty", http.StatusBadRequest)
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+proxyAddr+"/v1.47/containers/create", http.NoBody)
	assert.NilError(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	assert.Equal(t, string(respBody), "config cannot be empty\n")
}

func TestServeRelaysFinalResponseAfterContinue(t *testing.T) {
	backendAddr := startBackend(t, func(w http.ResponseWriter, req *http.Request) {
		// Reading the body of a request with `Expect: 100-continue` makes
		// the server send 100 Continue before the final response.
		if _, err := io.ReadAll(req.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "created")
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	var interim atomic.Int32
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(int, textproto.MIMEHeader) error {
			interim.Add(1)
			return nil
		},
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodPost, "http://"+proxyAddr+"/v1.47/images/load", strings.NewReader("tar"))
	assert.NilError(t, err)
	req.Header.Set("Expect", "100-continue")
	client := &http.Client{Transport: &http.Transport{ExpectContinueTimeout: testTimeout}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusCreated)
	assert.Equal(t, string(body), "created")
	// The proxy's server answers the client's Expect itself, so dockerd's own
	// 100 Continue must not reach the client as a second one.
	assert.Equal(t, interim.Load(), int32(1))
}

func TestServeClosesBackendConnectionAfterResponse(t *testing.T) {
	closeRequests := make(chan bool, 1)
	backendAddr := startBackend(t, func(w http.ResponseWriter, req *http.Request) {
		// req.Close reports whether the proxy sent Connection: close.
		closeRequests <- req.Close
		w.WriteHeader(http.StatusNoContent)
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+proxyAddr+"/v1.47/version", http.NoBody)
	assert.NilError(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusNoContent)
	select {
	case closeRequested := <-closeRequests:
		assert.Assert(t, closeRequested)
	case <-ctx.Done():
		assert.Assert(t, false, "the backend did not receive the request")
	}
}

func TestServeLeavesAcceptEncodingToClient(t *testing.T) {
	acceptEncodings := make(chan string, 1)
	backendAddr := startBackend(t, func(w http.ResponseWriter, req *http.Request) {
		acceptEncodings <- req.Header.Get("Accept-Encoding")
		w.WriteHeader(http.StatusNoContent)
	})
	proxyAddr := startProxy(t, backendAddr)
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+proxyAddr+"/v1.47/version", http.NoBody)
	assert.NilError(t, err)
	// http.DefaultClient would ask for gzip itself.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusNoContent)
	select {
	case acceptEncoding := <-acceptEncodings:
		assert.Equal(t, acceptEncoding, "")
	case <-ctx.Done():
		assert.Assert(t, false, "the backend did not receive the request")
	}
}

func TestServeTruncatedResponseFailsClient(t *testing.T) {
	backend := listenBackend(t)
	go func() {
		conn, err := backend.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := http.ReadRequest(bufio.NewReader(conn)); err != nil {
			return
		}
		// Close the connection partway through the chunked body.
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n")
	}()

	proxyAddr := startProxy(t, backend.Addr().String())
	ctx, cancel := context.WithTimeout(t.Context(), testTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+proxyAddr+"/v1.47/images/get?names=alpine", http.NoBody)
	assert.NilError(t, err)
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	assert.Equal(t, string(body), "hello")
}

func TestServeUpgradeHalfClose(t *testing.T) {
	backend := listenBackend(t)
	backendErr := make(chan error, 1)
	go func() {
		backendErr <- func() error {
			conn, err := backend.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
				return err
			}
			reader := bufio.NewReader(conn)
			if _, err := http.ReadRequest(reader); err != nil {
				return err
			}
			if _, err := io.WriteString(conn, "HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"); err != nil {
				return err
			}
			// Echo until the client half-closes, then half-close too.
			if _, err := io.Copy(conn, reader); err != nil {
				return err
			}
			return conn.(*net.TCPConn).CloseWrite()
		}()
	}()

	proxyAddr := startProxy(t, backend.Addr().String())
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "tcp", proxyAddr)
	assert.NilError(t, err)
	defer conn.Close()
	assert.NilError(t, conn.SetDeadline(time.Now().Add(testTimeout)))
	_, err = io.WriteString(conn, "POST /v1.47/containers/abc/attach?stream=1&stdin=1 HTTP/1.1\r\nHost: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	assert.NilError(t, err)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	assert.NilError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, resp.StatusCode, http.StatusSwitchingProtocols)

	_, err = io.WriteString(conn, "hello")
	assert.NilError(t, err)
	assert.NilError(t, conn.(*net.TCPConn).CloseWrite())
	echoed, err := io.ReadAll(reader)
	assert.NilError(t, err)
	assert.Equal(t, string(echoed), "hello")
	assert.NilError(t, <-backendErr)
}

func TestServeClientDisconnectClosesBackend(t *testing.T) {
	backend := listenBackend(t)
	backendErr := make(chan error, 1)
	go func() {
		backendErr <- func() error {
			conn, err := backend.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(testTimeout)); err != nil {
				return err
			}
			reader := bufio.NewReader(conn)
			if _, err := http.ReadRequest(reader); err != nil {
				return err
			}
			// Start a stream and keep it open, as `docker logs -f` does for a
			// quiet container.
			if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n"); err != nil {
				return err
			}
			// Returns io.EOF once the proxy closes the connection.
			_, err = reader.ReadByte()
			return err
		}()
	}()

	proxyAddr := startProxy(t, backend.Addr().String())
	var dialer net.Dialer
	conn, err := dialer.DialContext(t.Context(), "tcp", proxyAddr)
	assert.NilError(t, err)
	defer conn.Close()
	assert.NilError(t, conn.SetDeadline(time.Now().Add(testTimeout)))
	_, err = io.WriteString(conn, "GET /v1.47/containers/abc/logs?follow=1 HTTP/1.1\r\nHost: docker\r\n\r\n")
	assert.NilError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	assert.NilError(t, err)
	defer resp.Body.Close()
	_, err = io.ReadFull(resp.Body, make([]byte, 5))
	assert.NilError(t, err)
	assert.NilError(t, conn.Close())
	assert.ErrorIs(t, <-backendErr, io.EOF)
}
