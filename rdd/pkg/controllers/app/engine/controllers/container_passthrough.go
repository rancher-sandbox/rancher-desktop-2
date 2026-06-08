// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/gorilla/websocket"
	"github.com/moby/moby/api/pkg/stdcopy"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

const (
	controlTimeout = time.Second
	writeTimeout   = 10 * time.Second
	pingInterval   = 10 * time.Second
	pingTimeout    = time.Minute
)

var containerIDValidator = regexp.MustCompile(`^[0-9a-fA-F]+$`)

// websocketWriter implements [io.Writer] writing to a websocket connection,
// with each Write call resulting in a separate [websocket.BinaryMessage].
type websocketWriter struct {
	conn *websocket.Conn
}

func (w *websocketWriter) Write(p []byte) (int, error) {
	_ = w.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	writer, err := w.conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, err := writer.Write(p)
	if err == nil {
		err = writer.Close()
	}
	return n, err
}

// HandleLogs implements the log endpoint to pass through container logs.
// It is at `/passthrough/engine/logs/{container}[?tail=999]`.
func (r *EngineReconciler) HandleLogs(w http.ResponseWriter, req *http.Request) {
	log := ctrl.LoggerFrom(req.Context())
	containerID, _, _ := strings.Cut(strings.TrimLeft(req.URL.Path, "/"), "/")
	if !containerIDValidator.MatchString(containerID) {
		log.V(5).Info("Invalid container ID", "container", containerID)
		http.Error(w, "Invalid container ID", http.StatusBadRequest)
		return
	}
	log.V(5).Info("Handling logs for container", "containerID", containerID)

	var c containersv1alpha1.Container
	err := r.Client.Get(req.Context(), types.NamespacedName{
		Namespace: r.apiNamespace,
		Name:      containerID,
	}, &c)
	if err != nil {
		switch {
		case apierrors.IsNotFound(err):
			log.V(5).Info("Container not found", "container", containerID)
			http.Error(w, "Container not found", http.StatusNotFound)
		default:
			log.V(5).Info("Failed to get container", "container", containerID, "error", err)
			http.Error(w, "Failed to get container", http.StatusInternalServerError)
		}
		return
	}

	var opts []engineLogOptions

	if f := req.URL.Query().Get("follow"); f != "" {
		if follow, err := strconv.ParseBool(f); err == nil {
			opts = append(opts, engineLogWithFollow(follow))
		} else {
			log.V(5).Info("Invalid follow parameter", "follow", f, "error", err)
			http.Error(w, "Invalid follow parameter", http.StatusBadRequest)
			return
		}
	}
	if tail := req.URL.Query().Get("tail"); tail != "" {
		opts = append(opts, engineLogWithTail(tail))
	}

	r.watcherMu.Lock()
	watcher := r.watcher
	r.watcherMu.Unlock()
	if watcher == nil {
		log.V(5).Info("Docker watcher not running")
		http.Error(w, "Docker watcher not running", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	hasTTY, err := watcher.hasTTY(ctx, &c)
	if err != nil {
		switch {
		case errdefs.IsNotFound(err):
			log.V(5).Info("Container not found", "container", containerID)
			http.Error(w, "Container not found", http.StatusNotFound)
		case errdefs.IsInvalidArgument(err):
			log.V(5).Info("Invalid argument", "error", err)
			http.Error(w, "Invalid argument", http.StatusBadRequest)
		default:
			log.V(5).Info("Failed to inspect container", "error", err)
			http.Error(w, "Failed to inspect container", http.StatusInternalServerError)
		}
		return
	}

	reader, err := watcher.getLogs(ctx, &c, opts...)
	if err != nil {
		switch {
		case errdefs.IsNotFound(err):
			log.V(5).Info("Container not found", "container", containerID)
			http.Error(w, "Container not found", http.StatusNotFound)
		case errdefs.IsInvalidArgument(err):
			log.V(5).Info("Invalid argument", "error", err)
			http.Error(w, "Invalid argument", http.StatusBadRequest)
		default:
			log.V(5).Info("Failed to get container logs", "error", err)
			http.Error(w, "Failed to get container logs", http.StatusInternalServerError)
		}
		return
	}
	defer reader.Close()

	// Now that we have everything, we can do the websocket upgrade.  Defer this
	// to as late as possible so we can report container engine errors via HTTP.
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.V(5).Info("Failed to upgrade to WebSocket", "error", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(4096)
	defer func() {
		message := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "Closing connection")
		err := conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(controlTimeout))
		if err == nil {
			// Allow for websocket to close gracefully
			select {
			case <-time.After(controlTimeout):
			case <-ctx.Done():
				// We cancel the context on read or write error, which happens
				// when the connection is closed.  The `defer cancel()` from
				// when we created the context hasn't triggered yet.
			}
		} else if !errors.Is(err, websocket.ErrCloseSent) {
			log.V(5).Info("Failed to close WebSocket", "error", err)
		}
	}()

	// Set up a goroutine to handle reading from the connection; per gorilla
	// documentation, only one goroutine can call the read methods.
	go func() {
		timer := time.NewTimer(pingInterval)
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(pingTimeout))
			timer.Reset(pingInterval)
			return nil
		})
		_ = conn.SetReadDeadline(time.Now().Add(pingTimeout))

		// Set up a goroutine to emit ping messages
		go func() {
			for {
				select {
				case <-timer.C:
					err := conn.WriteControl(
						websocket.PingMessage,
						[]byte{},
						time.Now().Add(controlTimeout))
					if err != nil {
						// If we fail to write, the connection isn't usable
						// anymore; just cancel everything.
						cancel()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
		// Loop to read messages, so we can abort once we receive any errors.
		// On shutdown, ReadMessage will return an error, so we don't need an
		// explicit check for the context closing.
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				// Error includes [websocket.CloseError], i.e. graceful close.
				cancel()
				return
			}
			// On a successful read, reset the ping timer since there is no need
			// to fire a ping immediately after.
			_ = conn.SetReadDeadline(time.Now().Add(pingTimeout))
			timer.Reset(pingInterval)
		}
	}()

	writer := &websocketWriter{conn: conn}

	if hasTTY {
		_, err = io.Copy(writer, reader)
	} else {
		_, err = stdcopy.StdCopy(writer, writer, reader)
	}
	if err != nil {
		log.V(5).Info("Failed to copy container logs", "error", err)
		switch {
		case errors.Is(err, context.Canceled):
		case errors.Is(err, net.ErrClosed):
		case errors.Is(err, websocket.ErrCloseSent):
		case errors.As(err, new(*websocket.CloseError)):
		default:
			message := websocket.FormatCloseMessage(
				websocket.CloseInternalServerErr,
				fmt.Sprintf("failed to copy container logs: %s", err))
			err = conn.WriteControl(websocket.CloseMessage, message, time.Now().Add(writeTimeout))
			if err != nil {
				log.V(5).Info("Failed to close WebSocket", "error", err)
			}
		}
	}
}
