// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

// logrusLevelToKlog converts current logrus level to klog level.
func logrusLevelToKlog() string {
	switch logrus.GetLevel() {
	case logrus.DebugLevel:
		return "2"
	case logrus.TraceLevel:
		return "4"
	default:
		return "0"
	}
}

func newServiceCommand(ctx context.Context) *cobra.Command {
	command := &cobra.Command{
		Use:           "service",
		Short:         "Manage the RDD control plane management",
		Aliases:       []string{"svc"},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	command.AddCommand(
		newServiceConfigCommand(),
		newServiceCreateCommand(),
		newServiceStartCommand(),
		service.NewServeCommand(ctx),
		newServiceStopCommand(),
		newServiceDeleteCommand(),
		newServiceStatusCommand(),
		newServiceLogCommand(),
		newServicePathsCommand(),
	)
	return command
}

func newServiceConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Prints the kubeconfig for the RDD control plane",
		Args:  cobra.NoArgs,
		RunE:  serviceConfigAction,
	}
	return command
}

func ensureServiceRunning(ctx context.Context) error {
	if !service.Exists() {
		if err := service.Create(nil); err != nil {
			return err
		}
	}
	if service.Running() {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if err := service.Wait(ctx); err != nil {
			return err
		}
		return waitForDiscoveryConfigMap(ctx)
	}
	return startAndWaitForReady(ctx, nil)
}

// startAndWaitForReady starts the service, waits for the API server and the
// discovery ConfigMap to be ready. After an unclean shutdown the ConfigMap may
// survive with stale data, so the freshness check waits for a ConfigMap whose
// creationTimestamp is at or after the current startup.
//
// Both the API server readiness and ConfigMap freshness polls share a single
// 90-second timeout, because the apiserver may become ready before the
// serve command creates the ConfigMap.
func startAndWaitForReady(ctx context.Context, serveArgs []string) error {
	// Truncate to second precision because metav1.Time drops sub-seconds
	// during JSON serialization. Without this, a server that starts in the
	// same second would appear to have a startTime *before* beforeStart.
	beforeStart := time.Now().Truncate(time.Second)
	if err := service.Start(ctx, serveArgs); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	if err := service.Wait(ctx); err != nil {
		return err
	}
	return waitForFreshDiscoveryConfigMap(ctx, beforeStart)
}

// waitForDiscoveryConfigMap polls until the discovery ConfigMap exists.
func waitForDiscoveryConfigMap(ctx context.Context) error {
	return waitForFreshDiscoveryConfigMap(ctx, time.Time{})
}

// waitForFreshDiscoveryConfigMap polls until the discovery ConfigMap has a
// creationTimestamp at or after beforeStart. The serve command recreates the
// ConfigMap on every startup, so a fresh timestamp means this control plane
// instance is ready. Pass zero time to wait for existence only.
func waitForFreshDiscoveryConfigMap(ctx context.Context, beforeStart time.Time) error {
	restConfig, err := service.GetKubeRestConfig()
	if err != nil {
		return err
	}
	client, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return wait.PollUntilContextCancel(ctx, 500*time.Millisecond, true, func(ctx context.Context) (bool, error) {
		cm, err := client.CoreV1().ConfigMaps(controllers.RDDSystemNamespace).Get(
			ctx, controllers.ControllerManagerConfigMapName, metav1.GetOptions{},
		)
		if apierrors.IsNotFound(err) {
			return false, nil // ConfigMap not created yet; retry.
		}
		if err != nil {
			return false, err
		}
		return !cm.CreationTimestamp.Time.Before(beforeStart), nil
	})
}

func serviceConfigAction(cmd *cobra.Command, _ []string) error {
	if err := ensureServiceRunning(cmd.Context()); err != nil {
		return err
	}
	contents, err := service.GetKubeconfig()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(contents))
	return nil
}

func newServiceCreateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "create",
		Long: "Create RDD control plane",
		RunE: serviceCreateAction,
	}

	command.Flags().String("controllers", "*", "Controllers to enable. Use '*' for all, or specify comma-separated list. API groups: 'rdd' (configmapreplicaset,notary), 'app' (demo). Prefix with '-' to exclude, e.g., '*,-demo'")
	command.Flags().Int("secure-port", 0, "The port on which to serve HTTPS with authentication and authorization (default: 6443 + instance index)")
	if !developer.Mode() {
		_ = command.Flags().MarkHidden("controllers")
		_ = command.Flags().MarkHidden("secure-port")
	}
	return command
}

func serviceCreateAction(cmd *cobra.Command, args []string) error {
	if service.Exists() {
		logrus.Infof("%q control plane already exists", instance.Name())
		return nil
	}
	controllers, err := cmd.Flags().GetString("controllers")
	if err != nil {
		return err
	}
	args = append(args, "--controllers", controllers)
	if cmd.Flags().Changed("secure-port") {
		securePort, err := cmd.Flags().GetInt("secure-port")
		if err != nil {
			return err
		}
		args = append(args, "--secure-port", strconv.Itoa(securePort))
	}
	args = append(args, "-v", logrusLevelToKlog())

	if err := service.Create(args); err != nil {
		return err
	}
	logrus.Infof("successfully created %q control plane", instance.Name())
	return nil
}

func newServiceStartCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "start",
		Long: "Start RDD control plane. When called without parameters, uses default parameters from create. When called with parameters, those override the defaults for this session only.",
		RunE: serviceStartAction,
	}
	command.Flags().Bool("wait", true, "Wait for control plane to be ready")

	// Add serve command flags to start command so they can be passed through
	command.Flags().String("controllers", "", "Controllers to enable for this session (overrides create defaults)")
	command.Flags().Int("secure-port", 0, "The port on which to serve HTTPS with authentication and authorization (overrides create defaults)")
	if !developer.Mode() {
		_ = command.Flags().MarkHidden("controllers")
		_ = command.Flags().MarkHidden("secure-port")
	}
	return command
}

func serviceStartAction(cmd *cobra.Command, args []string) error {
	if !service.Exists() {
		if err := service.Create(args); err != nil {
			return err
		}
		logrus.Infof("successfully created %q control plane", instance.Name())
	}
	if service.Running() {
		logrus.Infof("%q control plane is already running", instance.Name())
		wait, err := cmd.Flags().GetBool("wait")
		if err == nil && wait {
			logrus.Infof("waiting for %q control plane to be ready", instance.Name())
			err = service.WaitWithTimeout(cmd.Context())
		}
		return err
	}

	// Collect all provided flags as arguments for serve subprocess
	var serveArgs []string
	if cmd.Flags().Changed("controllers") {
		controllers, err := cmd.Flags().GetString("controllers")
		if err != nil {
			return err
		}
		serveArgs = append(serveArgs, "--controllers", controllers)
	}
	if cmd.Flags().Changed("secure-port") {
		securePort, err := cmd.Flags().GetInt("secure-port")
		if err != nil {
			return err
		}
		serveArgs = append(serveArgs, "--secure-port", strconv.Itoa(securePort))
	}
	serveArgs = append(serveArgs, "-v", logrusLevelToKlog())
	serveArgs = append(serveArgs, args...)

	wait, err := cmd.Flags().GetBool("wait")
	if err == nil && wait {
		if err := startAndWaitForReady(cmd.Context(), serveArgs); err != nil {
			return err
		}
		logrus.Infof("%q control plane is ready", instance.Name())
	} else {
		if err := service.Start(cmd.Context(), serveArgs); err != nil {
			return err
		}
		logrus.Infof("%q control plane is starting", instance.Name())
	}
	return nil
}

func serviceStopAction(cmd *cobra.Command, _ []string) error {
	if !service.Exists() {
		logrus.Infof("%q control plane does not exist", instance.Name())
		return nil
	}
	if !service.Running() {
		logrus.Infof("%q control plane is already stopped", instance.Name())
		return nil
	}

	wait, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return err
	}

	if err := service.StopWithWait(wait); err != nil {
		return err
	}
	if wait {
		logrus.Infof("%q control plane has stopped", instance.Name())
	} else {
		logrus.Infof("%q control plane is stopping", instance.Name())
	}
	return nil
}

func newServiceStopCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "stop",
		Long: "Stop RDD control plane",
		RunE: serviceStopAction,
	}
	command.Flags().Bool("wait", true, "Wait for control plane to actually stop")
	return command
}

func newServiceDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "delete",
		Long: "Delete RDD control plane",
		RunE: func(*cobra.Command, []string) error {
			if !service.Exists() {
				logrus.Infof("%q control plane does not exist", instance.Name())
				return nil
			}
			if err := service.Delete(); err != nil {
				return err
			}
			logrus.Infof("%q control plane has been deleted", instance.Name())
			return nil
		},
	}
	return command
}

func newServiceStatusCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "status",
		Long: "Show control plane status",
		RunE: func(*cobra.Command, []string) error {
			logrus.SetLevel(logrus.InfoLevel)
			logrus.Infof("%q control plane has been created: %v", instance.Name(), service.Exists())
			logrus.Infof("%q control plane has been started: %v", instance.Name(), service.Running())
			logrus.Infof("%q control plane PID is: %v", instance.Name(), service.PID())
			if developer.Mode() {
				logrus.Info("developer mode is enabled")
			}
			return nil
		},
	}
	return command
}

func newServiceLogCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "log",
		Aliases: []string{"logs"},
		Long:    "Show control plane logs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			logrus.SetLevel(logrus.InfoLevel)

			name := "rdd.stderr.log"
			if ok, _ := cmd.Flags().GetBool("stdout"); ok {
				name = "rdd.stdout.log"
			}
			logPath := filepath.Join(instance.LogDir(), name)
			follow, _ := cmd.Flags().GetBool("follow")

			return tail.File(cmd.Context(), cmd.OutOrStdout(), logPath, follow)
		},
	}
	command.Flags().BoolP("stdout", "o", false, "Print stdout instead of stderr")
	command.Flags().BoolP("follow", "f", false, "Follow log output")
	return command
}
