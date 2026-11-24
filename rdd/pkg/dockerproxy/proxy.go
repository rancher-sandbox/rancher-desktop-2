// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package dockerproxy serves the Docker API on the Windows host and forwards
// each request to dockerd in the WSL2 guest. It rewrites the Windows host
// paths of bind mounts in container create requests to the paths where the
// guest mounts them, and relays requests, responses, and the upgraded
// connections of attach, exec, and build with httputil.ReverseProxy.
//
// After an upgrade, the proxy passes a half-close from either side on to the
// other, as long as both connections have a CloseWrite method. But a client
// that half-closes before an upgrade, or during a normal request, gets a 502
// if dockerd hasn't sent the response headers yet, and a response cut short
// if it has. net/http can't tell a half-close from a client that has gone
// away, so it cancels the request context, and httputil.ReverseProxy then
// cancels the request to dockerd. Detaching that request from the context
// would let the client get the whole response, but it would also keep a
// quiet stream, such as `docker logs -f` on an idle container, open to
// dockerd after its client has gone.
//
// A client must wait for dockerd's 101 response before it writes to an
// upgraded connection. Bytes that it sends earlier may already be in
// net/http's read buffer when the proxy hijacks the connection. But
// httputil.ReverseProxy copies only from the hijacked connection, not from
// that buffer, so those bytes never reach dockerd. The Docker CLI uses
// Moby's Go client, which waits for the 101.
package dockerproxy

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"time"

	"github.com/go-logr/logr"
)

// backendHost is the host name in each request that the proxy sends to
// dockerd. The connection comes from dial, so the name never needs to
// resolve; it only fills the Host header.
const backendHost = "api.moby.localhost"

// apiVersionPrefix matches the API version that clients put in front of the
// request path, as in /v1.47/containers/create.
var apiVersionPrefix = regexp.MustCompile(`^/v[0-9.]+/`)

// requestPath returns the request path without its API version prefix.
func requestPath(req *http.Request) string {
	requestPath := req.URL.Path
	if match := apiVersionPrefix.FindStringIndex(requestPath); match != nil {
		return requestPath[match[1]-1:]
	}
	return requestPath
}

// Serve accepts Docker API connections on listener and forwards each request
// over a new connection from dial. It stops when ctx is done, and then
// returns nil.
func Serve(ctx context.Context, logger logr.Logger, listener net.Listener, dial func(context.Context) (net.Conn, error)) error {
	errorLog := slog.NewLogLogger(logr.ToSlogHandler(logger), slog.LevelError)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme = "http"
			r.Out.URL.Host = backendHost
			r.Out.Host = ""
			// net/http's server answers the client's `Expect: 100-continue`
			// itself. If dockerd got the header too, it would send a 100
			// Continue of its own, and the proxy would relay that to the
			// client as a second one.
			r.Out.Header.Del("Expect")
			if r.Out.Method != http.MethodPost || requestPath(r.Out) != "/containers/create" {
				return
			}
			// If the body can't be decoded or translated, the request still
			// holds it as the client sent it. The proxy forwards it
			// untranslated and leaves dockerd to accept or reject it.
			if err := mungeContainersCreate(logger, r.Out); err != nil {
				logger.Error(err, "Could not translate container create request")
			}
		},
		// DisableKeepAlives closes a connection to dockerd when its
		// response ends. Otherwise the Transport would keep the idle
		// connection open with no timeout, even after Serve returns.
		// DisableKeepAlives doesn't apply to a connection that switches
		// protocols, as attach, exec, and build do, and server.Close
		// doesn't close the hijacked client connection at its other end.
		// So such a connection can stay open after Serve returns.
		//
		// DisableCompression stops the Transport from asking dockerd for
		// gzip when the client doesn't send an Accept-Encoding header. If
		// dockerd answered with gzip, the Transport would unzip the response
		// itself, and the client would get it without its Content-Length.
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dial(ctx)
			},
			DisableKeepAlives:  true,
			DisableCompression: true,
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			// net/http cancels the request context when the client goes
			// away or half-closes. The error then only says that the request
			// was cancelled, so it isn't logged.
			if req.Context().Err() == nil {
				logger.Error(err, "Could not forward request to dockerd", "method", req.Method, "path", req.URL.Path)
			}
			http.Error(w, "could not forward the request to dockerd: "+err.Error(), http.StatusBadGateway)
		},
		ErrorLog: errorLog,
	}
	server := &http.Server{
		ReadHeaderTimeout: time.Minute,
		Handler:           proxy,
		ErrorLog:          errorLog,
	}
	stop := context.AfterFunc(ctx, func() {
		server.Close()
	})
	defer stop()

	logger.Info("Listening", "address", listener.Addr().String())
	err := server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
