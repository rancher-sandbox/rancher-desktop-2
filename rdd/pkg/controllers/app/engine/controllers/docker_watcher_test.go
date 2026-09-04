// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/jsonstream"
	mobyclient "github.com/moby/moby/client"
	"gotest.tools/v3/assert"
)

// newTestDockerWatcher creates a dockerWatcher whose client talks to the
// given httptest.Server instead of a real Docker daemon.
func newTestDockerWatcher(t *testing.T, server *httptest.Server) *dockerWatcher {
	t.Helper()

	// server.URL is of the form "http://127.0.0.1:port"; the moby client
	// expects a "tcp://host:port" host so it configures a plain TCP+HTTP
	// transport (rather than a unix socket or named pipe).
	host := "tcp://" + strings.TrimPrefix(server.URL, "http://")
	cli, err := mobyclient.New(mobyclient.WithHost(host))
	assert.NilError(t, err)
	t.Cleanup(func() { _ = cli.Close() })

	return &dockerWatcher{cli: cli}
}

// TestDockerWatcherPullImageStreamErrorClassification verifies that
// pullImage's onComplete callback receives an error that is correctly
// classified by cerrdefs.Is*, based on the errorDetail.code reported mid-
// stream by the Docker daemon (as opposed to a synchronous error from the
// initial ImagePull call).
func TestDockerWatcherPullImageStreamErrorClassification(t *testing.T) {
	tests := []struct {
		name          string
		errorCode     int
		errorMessage  string
		expectedError error
	}{
		{
			name:          "invalid argument",
			errorCode:     http.StatusBadRequest,
			errorMessage:  "manifest unknown",
			expectedError: cerrdefs.ErrInvalidArgument,
		},
		{
			name:          "unauthorized",
			errorCode:     http.StatusUnauthorized,
			errorMessage:  "unauthorized: authentication required",
			expectedError: cerrdefs.ErrUnauthenticated,
		},
		{
			name:          "no code, generic failure",
			errorCode:     0,
			errorMessage:  "some other pull failure",
			expectedError: errors.New("some other pull failure"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				assert.NilError(t, json.NewEncoder(w).Encode(jsonstream.Message{
					Error: &jsonstream.Error{
						Code:    tc.errorCode,
						Message: tc.errorMessage,
					},
				}))
			}))
			defer server.Close()

			w := newTestDockerWatcher(t, server)

			errCh := make(chan error, 1)
			err := w.pullImage(t.Context(), "alpine:latest", nil, func(err error) {
				errCh <- err
			})
			assert.NilError(t, err)

			select {
			case completeErr := <-errCh:
				assert.Assert(t, completeErr != nil)
				assert.Equal(t, cerrdefs.IsInvalidArgument(completeErr), cerrdefs.IsInvalidArgument(tc.expectedError))
				assert.Equal(t, cerrdefs.IsUnauthorized(completeErr), cerrdefs.IsUnauthorized(tc.expectedError))
			case <-time.After(10 * time.Second):
				//nolint:forbidigo // Not making an assertion, use t.Fatal directly.
				t.Fatal("timed out waiting for onComplete to be called")
			}
		})
	}
}
