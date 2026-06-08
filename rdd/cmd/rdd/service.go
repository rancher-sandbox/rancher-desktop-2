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
	watchtools "k8s.io/client-go/tools/watch"

	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

// startWaitTimeout bounds the wait for the API server, CRDs, and
// controller-manager registration to land. Cold start can take well over 30s
// in CI on slower runners.
const startWaitTimeout = 90 * time.Second

// stopWaitTimeout bounds the CLI wait for the control plane to shut down.
// Run caps the internal drain at 45s (see pkg/service/cmd/service.go).
// The CLI deadline exceeds that cap to absorb slow disks, misconfigured
// hosts, and sequential multi-VM drains. Aliased to limaLongWaitTimeout
// (defined in limavm.go) so the per-VM ceiling stays the single tuning
// knob for long CLI waits.
const stopWaitTimeout = limaLongWaitTimeout

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

// ensureServiceRunning readies the control plane before a CLI command
// relies on it. It bounds the already-running and cold-start paths by
// startWaitTimeout (90s), independent of the caller's --timeout. The
// cap lets a broken service fail fast so rdd limavm *, rdd set, rdd
// kubectl, and rdd service config stay responsive on a long deadline.
func ensureServiceRunning(ctx context.Context) error {
	if !service.Exists() {
		// Persist no extra args; the per-start klogArgs below override
		// verbosity on every start, so baking the caller's logrus level
		// into args.json would only mislead future inspectors of the file.
		if err := service.Create(nil); err != nil {
			return err
		}
	}
	if service.Running() {
		startTime, err := service.StartTime()
		if err != nil {
			return err
		}
		ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, startWaitTimeout)
		defer cancel()
		if err := service.Wait(ctx); err != nil {
			return cliexit.Classify(err)
		}
		return cliexit.Classify(waitForFreshDiscoveryConfigMap(ctx, startTime))
	}
	// Pass klog verbosity transiently so V(1) tracing fires when
	// autostart launches the daemon (e.g. from `rdd set`).
	return cliexit.Classify(startAndWaitForReady(ctx, []string{"-v", logrusLevelToKlog()}, startWaitTimeout))
}

// startAndWaitForReady starts the service, waits for the API server and the
// discovery ConfigMap to be ready. After an unclean shutdown the ConfigMap may
// survive with stale data, so the freshness check waits for a ConfigMap whose
// creationTimestamp is at or after the current startup.
//
// Pass timeout 0 to wait indefinitely. A finite timeout bounds both the API
// server readiness poll and the ConfigMap freshness poll together, because
// the apiserver may become ready before the serve command creates the ConfigMap.
func startAndWaitForReady(ctx context.Context, serveArgs []string, timeout time.Duration) error {
	// Truncate to second precision because metav1.Time drops sub-seconds
	// during JSON serialization. Without this, a server that starts in the
	// same second would appear to have a startTime *before* beforeStart.
	beforeStart := time.Now().Truncate(time.Second)
	if err := service.Start(ctx, serveArgs); err != nil {
		return err
	}
	logrus.Infof("starting %q control plane", instance.Name())

	ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
	defer cancel()

	if err := service.Wait(ctx); err != nil {
		return err
	}
	return waitForFreshDiscoveryConfigMap(ctx, beforeStart)
}

// waitForFreshDiscoveryConfigMap polls until the discovery ConfigMap
// for the current control plane instance exists and is marked ready.
// The serve command recreates the ConfigMap on every startup, so a
// creationTimestamp at or after beforeStart identifies the current
// instance. The ready annotation is set after CRDs are installed and
// every controller manager has registered, so waiting for it lets
// clients use both CRDs and discovery data without racing startup.
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
		if cm.CreationTimestamp.Time.Before(beforeStart) {
			return false, nil // Stale ConfigMap from a previous run; wait for the new one.
		}
		return cm.Annotations[controllers.ReadyAnnotation] == "true", nil
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

	command.Flags().String("controllers", "*", controllers.ControllersFlagUsage)
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
	command.Flags().Duration("timeout", startWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")

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
	waitFlag, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return err
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return err
	}

	if !service.Exists() {
		if err := service.Create(args); err != nil {
			return err
		}
		logrus.Infof("successfully created %q control plane", instance.Name())
	}
	if service.Running() {
		logrus.Infof("%q control plane is already running", instance.Name())
		if !waitFlag {
			return nil
		}
		startTime, err := service.StartTime()
		if err != nil {
			return err
		}
		logrus.Infof("waiting for %q control plane to be ready", instance.Name())
		ctx, cancel := watchtools.ContextWithOptionalTimeout(cmd.Context(), timeout)
		defer cancel()
		if err := service.Wait(ctx); err != nil {
			return cliexit.Classify(err)
		}
		if err := waitForFreshDiscoveryConfigMap(ctx, startTime); err != nil {
			return cliexit.Classify(err)
		}
		logrus.Infof("%q control plane is ready", instance.Name())
		return nil
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

	if waitFlag {
		if err := startAndWaitForReady(cmd.Context(), serveArgs, timeout); err != nil {
			return cliexit.Classify(err)
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

	waitFlag, err := cmd.Flags().GetBool("wait")
	if err != nil {
		return err
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return err
	}

	if err := service.StopWithWait(cmd.Context(), waitFlag, timeout); err != nil {
		return cliexit.Classify(err)
	}
	if waitFlag {
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
	command.Flags().Duration("timeout", stopWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")
	return command
}

func newServiceDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:  "delete",
		Long: "Delete RDD control plane",
		RunE: serviceDeleteAction,
	}
	command.Flags().Duration("timeout", stopWaitTimeout, "Timeout for the control plane to exit before deletion (0 means wait indefinitely)")
	return command
}

func serviceDeleteAction(cmd *cobra.Command, _ []string) error {
	if !service.Exists() {
		logrus.Infof("%q control plane does not exist", instance.Name())
		return nil
	}
	timeout, err := cmd.Flags().GetDuration("timeout")
	if err != nil {
		return err
	}
	if err := service.Delete(cmd.Context(), timeout); err != nil {
		return cliexit.Classify(err)
	}
	logrus.Infof("%q control plane has been deleted", instance.Name())
	return nil
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
