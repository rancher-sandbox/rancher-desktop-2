// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func newRunCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "run COMMAND [ARGS...]",
		Short: "Run a command wired to this instance's Docker and Kubernetes contexts",
		Long: help.Doc(`
			Run a command against this Rancher Desktop instance.

			rdd run points the command at the instance without changing
			your selected contexts: it prepends ~/.rd<instance>/bin to
			PATH, sets the Docker context to rancher-desktop-<instance>,
			and points KUBECONFIG at ~/.rd<instance>/kube.config, whose
			current context is rancher-desktop-<instance>. rdd run itself
			leaves your selected Docker context and the current context in
			~/.kube/config unchanged.

			Rancher Desktop starts first if it is not already running. A
			normal startup merges the rancher-desktop-<instance> entry
			into ~/.kube/config and creates its Docker context; it switches
			your current Docker or kube context only when the existing one
			is missing or not working. When the App does not exist yet and
			the command is kubectl or helm, rdd run enables Kubernetes so
			the command has a cluster to talk to. An existing App is only
			started, never reconfigured.

			Examples:
			  rdd run docker run --rm hello-world
			  rdd run kubectl get nodes
		`),
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE:               runAction,
	}
	return command
}

func runAction(cmd *cobra.Command, args []string) error {
	// Show help without starting the App. DisableFlagParsing otherwise routes
	// --help through ensureAppRunning and on to an exec of "--help" as a command.
	// A leading "--" still escapes it, so `rdd run -- --help` runs --help.
	if args[0] == "-h" || args[0] == "--help" {
		return cmd.Help()
	}
	// Drop a leading "--" separator, e.g. `rdd run -- docker ps`.
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("run requires a command to execute")
	}

	if err := ensureAppRunning(cmd.Context(), args[0]); err != nil {
		return err
	}

	if err := setupRunEnv(); err != nil {
		return err
	}

	return execCommand(cmd.Context(), args)
}

// ensureAppRunning starts Rancher Desktop and waits for it to settle. When the
// App does not exist yet and command invokes a Kubernetes client, it enables
// Kubernetes so the client has a cluster to talk to; the App defaulter then
// picks the default Kubernetes version. An existing App is only started.
func ensureAppRunning(ctx context.Context, command string) error {
	c, _, err := getAppKubeClient(ctx)
	if err != nil {
		return err
	}

	props := []string{"running=true"}
	var app appv1alpha1.App
	switch err := c.Get(ctx, client.ObjectKey{Name: "app"}, &app); {
	case apierrors.IsNotFound(err):
		if isKubeCommand(command) {
			props = append(props, "kubernetes.enabled=true")
		}
	case err != nil:
		return fmt.Errorf("failed to get App: %w", err)
	}

	return setAction(ctx, props, false, true, limaLongWaitTimeout)
}

// isKubeCommand reports whether name invokes kubectl or helm, ignoring any
// directory prefix and a trailing .exe.
func isKubeCommand(name string) bool {
	base := strings.TrimSuffix(filepath.Base(name), ".exe")
	return base == "kubectl" || base == "helm"
}

// setupRunEnv points this process's environment at the instance, so the child
// command inherits it. It prepends the instance bin directory to PATH, selects
// the instance Docker context, points KUBECONFIG at the instance kubeconfig,
// and clears DOCKER_HOST so the Docker context takes effect.
func setupRunEnv() error {
	binDir := filepath.Join(instance.ShortDir(), "bin")
	// Append the existing PATH only when it is set; concatenating an empty
	// value would leave a trailing separator, which POSIX reads as the
	// current directory.
	path := binDir
	if existing := os.Getenv("PATH"); existing != "" {
		path += string(os.PathListSeparator) + existing
	}
	vars := []struct{ key, value string }{
		{"PATH", path},
		// DOCKER_CONTEXT resolves against the child's DOCKER_CONFIG. The daemon
		// wrote this context under its own DOCKER_CONFIG, frozen at startup, so
		// the two must match for the lookup to succeed.
		{"DOCKER_CONTEXT", instance.Name()},
		{"KUBECONFIG", instance.KubeConfig()},
	}
	for _, v := range vars {
		if err := os.Setenv(v.key, v.value); err != nil {
			return fmt.Errorf("set %s: %w", v.key, err)
		}
	}
	if err := os.Unsetenv("DOCKER_HOST"); err != nil {
		return fmt.Errorf("unset DOCKER_HOST: %w", err)
	}
	return nil
}

// execCommand runs args as a child process with the current environment and
// propagates its exit status. rdd runs the command as a child, rather than
// replacing itself, so it propagates the child's exit code the same way on
// every platform. The child shares rdd's stdio and foreground process group,
// so Ctrl+C reaches it directly.
//
// CommandContext would hard-kill the child if ctx were canceled; the cobra
// context is never canceled today, so this matches a plain exec until
// signal-driven cancellation is wired up.
func execCommand(ctx context.Context, args []string) error {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err := command.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A signal-killed child has no exit code: ExitCode() returns -1, which
		// os.Exit maps to 255 rather than the shell's 128+signal. Wiring up
		// signal handling (see above) would let us propagate 128+signal.
		return &cliexit.Error{Code: exitErr.ExitCode()}
	}
	if err != nil {
		return fmt.Errorf("run %s: %w", args[0], err)
	}
	return nil
}
