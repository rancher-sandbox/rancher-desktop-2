// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package steve runs Rancher's steve API server with the Cluster Explorer
// dashboard in front of it.
//
// Steve v0.9.23 does not compile against k8s 0.37, because its
// AccessSetAuthorizer lacks the ConditionsAwareAuthorize and EvaluateConditions
// methods that authorizer.Authorizer has there. Moving rdd to k8s 0.37 takes
// steve v0.10.9, or a local copy of steve's pkg/server that drops its pkg/ext
// import.
package steve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/rancher/steve/pkg/server"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/ratelimit"
	"github.com/sirupsen/logrus"
)

// Options selects the cluster that steve connects to.
type Options struct {
	Kubeconfig string
	Context    string
}

// Run serves steve and the dashboard on a random localhost port until ctx ends.
func Run(ctx context.Context, opts Options) error {
	restConfig, err := kubeconfig.GetNonInteractiveClientConfigWithContext(opts.Kubeconfig, opts.Context).ClientConfig()
	if err != nil {
		return err
	}
	restConfig.RateLimiter = ratelimit.None

	// Steve's pkg/ui would fetch the UI from releases.rancher.com. Only
	// pkg/server/cli wires it in, so calling server.New directly leaves it out.
	// Unknown paths are 404s; the dashboard's /v3/settings probes rely on that.
	s, err := server.New(ctx, restConfig, nil)
	if err != nil {
		return err
	}

	// IPv4 only, to match the dashboard files.
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return err
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	var handler http.Handler = s
	if dashboardFiles == nil {
		logrus.Warn("This binary does not embed the dashboard; serving only the steve API")
	} else if handler, err = newHandler(s, dashboardFiles, port); err != nil {
		return fmt.Errorf("could not set up dashboard handler: %w", err)
	}

	// pkg/rancher-desktop/backend/steve.ts parses the port when stdout closes,
	// so nothing else may be written there.
	if _, err := fmt.Fprintf(os.Stdout, "%d\n", port); err != nil {
		return err
	}
	if err := os.Stdout.Close(); err != nil {
		return err
	}
	os.Stdout = os.Stderr
	logrus.Infof("Steve is listening on port %d", port)

	s.StartAggregation(ctx)

	srv := &http.Server{Handler: handler}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(timeoutCtx); err != nil {
			logrus.Errorf("error shutting down server: %v", err)
		}
	}()
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}
