// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
)

func newStartCommand() *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)
	command := &cobra.Command{
		Use:   "start",
		Short: "Start Rancher Desktop",
		Long: help.Doc(`
			Start Rancher Desktop, which provides a container engine
			and, when Kubernetes is enabled, a Kubernetes cluster with
			its context merged into ~/.kube/config.

			By default, rdd start waits until Rancher Desktop is ready
			to use. Pass --wait=false to return as soon as the request
			is queued, or --timeout=0 to wait indefinitely.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setAction(cmd.Context(), []string{"running=true"}, false, wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true,
		"Wait until Rancher Desktop is ready before returning")
	command.Flags().DurationVar(&timeout, "timeout", 300*time.Second,
		"Timeout for waiting (0 to wait indefinitely)")
	return command
}

func newStopCommand() *cobra.Command {
	var (
		wait    bool
		timeout time.Duration
	)
	command := &cobra.Command{
		Use:   "stop",
		Short: "Stop Rancher Desktop",
		Long: help.Doc(`
			Stop Rancher Desktop.

			By default, rdd stop waits until Rancher Desktop has fully
			shut down. Pass --wait=false to return as soon as the
			request is queued, or --timeout=0 to wait indefinitely.
		`),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return stopAction(cmd.Context(), wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true,
		"Wait until Rancher Desktop has shut down before returning")
	command.Flags().DurationVar(&timeout, "timeout", 300*time.Second,
		"Timeout for waiting (0 to wait indefinitely)")
	return command
}

// stopAction short-circuits when the App does not exist; otherwise it
// delegates to setAction so the wait semantics match `rdd set`.
func stopAction(ctx context.Context, wait bool, timeout time.Duration) error {
	c, _, err := getAppKubeClient(ctx)
	if err != nil {
		return err
	}
	var app appv1alpha1.App
	if err := c.Get(ctx, client.ObjectKey{Name: "app"}, &app); err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Info("App does not exist; nothing to stop")
			return nil
		}
		return fmt.Errorf("failed to get App: %w", err)
	}
	return setAction(ctx, []string{"running=false"}, false, wait, timeout)
}
