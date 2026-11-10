// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package steve runs Rancher's steve API server. Builds with the with_dashboard
// tag also embed the Cluster Explorer dashboard and serve it in front of steve.
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

// Run serves steve, and the dashboard if the build embeds it, on a random
// localhost port until ctx ends.
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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

	return serve(ctx, listener, handler)
}

// serve serves handler on listener until ctx ends or serving fails, and
// returns once the server has shut down.
func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	ctx, cancel := context.WithCancel(ctx)
	srv := &http.Server{Handler: handler}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		timeoutCtx, cancelTimeout := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelTimeout()
		if err := srv.Shutdown(timeoutCtx); err != nil {
			logrus.Errorf("error shutting down server: %v", err)
		}
	}()
	err := srv.Serve(listener)
	cancel()
	<-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
