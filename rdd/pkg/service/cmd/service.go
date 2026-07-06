// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	apiextensionapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	// Justify blank import.
	_ "k8s.io/apiserver/pkg/admission"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	genericapiserver "k8s.io/apiserver/pkg/server"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/apiserver/pkg/util/notfoundhandler"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	watchtools "k8s.io/client-go/tools/watch"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	logsapi "k8s.io/component-base/logs/api/v1"
	// Justify blank import.
	_ "k8s.io/component-base/metrics/prometheus/workqueue"
	"k8s.io/component-base/term"
	"k8s.io/component-base/version"
	"k8s.io/component-base/version/verflag"
	"k8s.io/klog/v2"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
	// Justify blank import.
	_ "k8s.io/kubernetes/pkg/features"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/binlinks"
	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
	// Import controller packages to trigger init() functions for embedded mode.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/app"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/demo"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/engine"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/kubernetes"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	// Import built-in system controllers.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/builtin/namespace"
	// Import controller packages to trigger init() functions for embedded mode.
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/containers"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/lima/limavm"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/configmapreplicaset"
	_ "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/notary"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/developer"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd/options"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/datastore"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/readiness"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/logfile"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/wsl"
)

// API groups that RDD requires and enables.
var requiredAPIGroups = sets.NewString(
	"apiextensions.k8s.io",         // CRDs
	"authorization.k8s.io",         // Authorization
	"events.k8s.io",                // Events
	"rbac.authorization.k8s.io",    // RBAC
	"admissionregistration.k8s.io", // Admission
	"coordination.k8s.io",          // Coordination (for controller-runtime compatibility)
)

// GetKubeconfig returns the kubeconfig by reading it directly from disk.
func GetKubeconfig() ([]byte, error) {
	if !Running() {
		return nil, fmt.Errorf("control plane %q is not running", instance.Name())
	}
	data, err := os.ReadFile(instance.Config())
	if err != nil {
		return nil, fmt.Errorf("could not read config from %s: %w (control plane may still be starting)", instance.Config(), err)
	}
	return data, nil
}

// GetKubeRestConfig generates and returns the kubeconfig as a *rest.Config.
func GetKubeRestConfig() (*rest.Config, error) {
	kubeConfigData, err := GetKubeconfig()
	if err != nil {
		return nil, err
	}
	return clientcmd.RESTConfigFromKubeConfig(kubeConfigData)
}

// storeKubeConfigToDisk stores the actual kubeconfig YAML to disk.
func storeKubeConfigToDisk(adminToken, userToken, serverURL, tlsServerName string, caCert []byte) error {
	kubeConfig := options.CreateKubeConfig(adminToken, userToken, serverURL, tlsServerName, caCert)
	data, err := clientcmd.Write(*kubeConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal kubeconfig: %w", err)
	}
	if err := os.WriteFile(instance.Config(), data, 0o600); err != nil {
		return fmt.Errorf("failed to write config to %s: %w", instance.Config(), err)
	}
	return nil
}

// Order for settings:
// Options -> CompletedOptions -> Config -> CompletedConfig -> Server -> Prepared -> Run

func init() {
	utilruntime.Must(logsapi.AddFeatureGates(utilfeature.DefaultMutableFeatureGate))
}

// Exists reports whether a control plane instance has been created.
func Exists() bool {
	_, err := os.Stat(instance.ArgsFile())
	return err == nil
}

// PIDNotFound indicates no running service process was found.
const PIDNotFound = 0

// PID returns the process ID of the running service, or PIDNotFound if it is
// not running. On Windows it confirms the PID via the daemon's interrupt event
// (process.IsOurProcess); on Unix it checks the PID is live.
func PID() int {
	pidStr, err := os.ReadFile(instance.PIDFile())
	if err != nil {
		return PIDNotFound
	}
	pid, err := strconv.Atoi(string(pidStr))
	if err != nil {
		// Unusable pidfile; drop it.
		_ = os.Remove(instance.PIDFile())
		return PIDNotFound
	}
	// Drop the file unless it names our live control plane. A dead, recycled, or
	// unrelated PID is not running, and the next Start writes a fresh file.
	if !process.IsOurProcess(process.ServeInterruptKey, pid) {
		_ = os.Remove(instance.PIDFile())
		return PIDNotFound
	}
	return pid
}

// Running reports whether the service process is alive.
func Running() bool {
	return PID() != PIDNotFound
}

// StartTime returns the start time of the running service, read
// from the PID file's mtime. Use it to anchor freshness checks for
// state the service creates after startup.
func StartTime() (time.Time, error) {
	fi, err := os.Stat(instance.PIDFile())
	if err != nil {
		return time.Time{}, err
	}
	return fi.ModTime().Truncate(time.Second), nil
}

// Create a new control plane instance with the given arguments.
func Create(args []string) error {
	if Exists() {
		return fmt.Errorf("%q control plane already exists", instance.Name())
	}
	if err := os.MkdirAll(instance.Dir(), 0o700); err != nil {
		return err
	}
	// Add default secure port first, then append user args (which may override it if specified).
	securePort := 6443 + instance.Index()
	args = append([]string{"--secure-port", strconv.Itoa(securePort)}, args...)

	data, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(instance.ArgsFile(), data, 0o600)
}

// checkSupportedVersion checks if the control plane is running a version that
// is compatible with this client.
func checkSupportedVersion(config *rest.Config) error {
	if developer.Mode() {
		// Skip the version check in developer mode, to make working on the
		// client against a constantly running server easier.
		return nil
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	serverVersion, err := client.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to get server version: %w", err)
	}

	// Currently, we only support the version that is the exact version of the
	// client.
	if serverVersion.GitVersion != version.Get().GitVersion {
		return &cliexit.Error{
			Code: cliexit.CodeIncompatibleServer,
			Err: fmt.Errorf("unsupported server version %s (expected %s)",
				serverVersion.GitVersion, version.Get().GitVersion),
		}
	}
	return nil
}

// getRuntimeControllersFromCluster retrieves all enabled controllers from the cluster.
func getRuntimeControllersFromCluster(ctx context.Context, config *rest.Config) ([]string, error) {
	if config == nil {
		return nil, errors.New("nil config provided")
	}

	discovery, err := controllers.NewControllerManagerDiscovery(config)
	if err != nil {
		klog.V(2).Infof("getRuntimeControllersFromCluster: discovery creation error: %v", err)
		return nil, fmt.Errorf("could not create discovery client: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	enabledControllers, err := discovery.GetEnabledControllers(ctx)
	if err != nil {
		klog.V(2).Infof("getRuntimeControllersFromCluster: discovery error: %v", err)
		return nil, fmt.Errorf("could not discover enabled controllers: %w", err)
	}
	return enabledControllers, nil
}

// Start the service in a background subprocess. Callers must verify Exists()
// first; Start assumes the instance exists.
func Start(ctx context.Context, args []string) error {
	if Running() {
		return fmt.Errorf("%q control plane is already running", instance.Name())
	}

	keepLogs := os.Getenv("RDD_KEEP_LOGS") != ""
	title := os.Getenv("RDD_LOG_TITLE")
	var header string
	if title != "" {
		header = "=== " + title + " ===\n"
	}
	stdout, err := logfile.Create(instance.LogDir(), "rdd.stdout", keepLogs, header)
	if err != nil {
		return err
	}
	stderr, err := logfile.Create(instance.LogDir(), "rdd.stderr", keepLogs, header)
	if err != nil {
		return err
	}

	cmdArgs := []string{"service", "serve"}
	// Always start with saved args from create (contains --secure-port)
	savedArgs := ServeArgs()
	cmdArgs = append(cmdArgs, savedArgs...)
	// Then add any additional args provided (e.g., --controllers override)
	cmdArgs = append(cmdArgs, args...)

	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, cmdArgs...)
	process.SetGroup(cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Start()
}

// startupPhase identifies the control-plane startup stage the readiness
// check is waiting on, so callers can report progress as the phase advances.
type startupPhase int

const (
	phaseAPIServer startupPhase = iota
	phaseControllers
	phaseCRDs
)

// String returns the user-facing progress message for the phase.
func (p startupPhase) String() string {
	switch p {
	case phaseAPIServer:
		return "waiting for API server"
	case phaseControllers:
		return "waiting for controllers to register"
	case phaseCRDs:
		return "waiting for CRDs to be established"
	default:
		return ""
	}
}

// checkReadiness performs a single readiness check, reporting via onPhase the
// phase it ends up waiting on.
func checkReadiness(ctx context.Context, onPhase func(startupPhase)) error {
	config, err := GetKubeRestConfig()
	if err != nil {
		onPhase(phaseAPIServer)
		klog.V(2).Infof("Waiting for kubeconfig to be available: %v", err)
		return err
	}

	if err := checkSupportedVersion(config); err != nil {
		onPhase(phaseAPIServer)
		klog.V(2).Infof("%v", err)
		return err
	}

	// Wait for the controller manager to register with the actual running controllers
	runtimeControllers, err := getRuntimeControllersFromCluster(ctx, config)
	if err != nil {
		onPhase(phaseControllers)
		klog.V(2).Infof("getRuntimeControllersFromCluster: %v", err)
		return err
	}

	if runtimeControllers == nil {
		// Discovery ConfigMap doesn't exist yet; the serve subprocess hasn't
		// finished initializing. Keep polling.
		onPhase(phaseControllers)
		klog.V(2).Info("Discovery configmap not found yet - waiting for control plane initialization")
		return errors.New("waiting for controller manager registration")
	}

	onPhase(phaseCRDs)
	if len(runtimeControllers) == 0 {
		// ConfigMap exists but no controllers are registered.
		klog.V(2).Info("No controllers registered - checking API server readiness")
		return readiness.WaitForReadyWithCRDs(ctx, config, []base.Controller{}, false)
	}

	klog.V(2).Infof("Waiting for %d runtime controllers to be ready", len(runtimeControllers))

	// Get the controller objects for the actually running controllers
	allControllers := base.GetAllControllers()
	var enabledControllers []base.Controller

	for _, controller := range allControllers {
		if slices.Contains(runtimeControllers, controller.GetName()) {
			enabledControllers = append(enabledControllers, controller)
		}
	}

	klog.V(2).InfoS("Discovery service found running controllers", "controllers", runtimeControllers)

	// Debug: Log all available controllers
	allControllerNames := make([]string, len(allControllers))
	for i, c := range allControllers {
		allControllerNames[i] = c.GetName()
	}
	klog.V(2).InfoS("All available controllers", "controllers", allControllerNames)

	// Debug: Log enabled controllers
	enabledControllerNames := make([]string, len(enabledControllers))
	for i, c := range enabledControllers {
		enabledControllerNames[i] = c.GetName()
	}
	klog.V(2).InfoS("Enabled controllers for readiness", "controllers", enabledControllerNames)

	return readiness.WaitForReadyWithCRDs(ctx, config, enabledControllers, false)
}

// Wait until the control plane is ready or the context is cancelled.
func Wait(ctx context.Context) error {
	// Fast path: stay silent when the control plane is already ready.
	if err := checkReadiness(ctx, func(startupPhase) {}); err == nil {
		return nil
	}

	// Log each startup phase once, as it becomes the phase we wait on.
	// Phases only advance, so a transient regression does not re-log an
	// earlier phase.
	lastPhase := startupPhase(-1)
	reportPhase := func(p startupPhase) {
		if p > lastPhase {
			lastPhase = p
			logrus.Info(p.String())
		}
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := checkReadiness(ctx, reportPhase)
			if err == nil {
				return nil
			}
			var cliError *cliexit.Error
			if errors.As(err, &cliError) && cliError.Code == cliexit.CodeIncompatibleServer {
				// The error will never recover; stop waiting.
				return err
			}
		}
	}
}

// StopWithWait sends a shutdown signal to the service. When wait is true, it
// blocks until the process exits, ctx is cancelled, or timeout elapses; pass
// timeout 0 to wait indefinitely (bounded only by ctx).
func StopWithWait(ctx context.Context, wait bool, timeout time.Duration) error {
	// Read the PID once and reject PIDNotFound before signalling. A second PID()
	// call can return PIDNotFound when the daemon exits, a concurrent stop removes
	// the file, or the identity check fails between the two reads; signalling 0
	// would then misfire — kill(0) hits the caller's whole process group on Unix.
	// (On Windows Interrupt(0) only fails to open a per-PID event, but reading
	// once is still the simpler contract.)
	pid := PID()
	if pid == PIDNotFound {
		return fmt.Errorf("%q control plane is not running", instance.Name())
	}

	// Try graceful shutdown first; Interrupt asks the daemon to run its orderly
	// shutdown. Kill is the fallback when Interrupt fails. A hard kill skips the
	// controller-manager drain, so hostagents survive in their own process groups
	// and self-heal on the next service start via killOrphanedHostagent.
	if err := process.Interrupt(process.ServeInterruptKey, pid); err != nil {
		if err := process.Kill(pid); err != nil {
			return fmt.Errorf("failed to stop %q control plane with pid %d: %w", instance.Name(), pid, err)
		}
	}

	if wait {
		ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
		defer cancel()
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Deadline fired or caller cancelled; terminate either way
				// to ensure the service process exits. Kill targets the
				// service alone: on Windows (TerminateProcess) it spares
				// hostagents, which live in their own process groups. On
				// Unix, Kill delivers SIGTERM, which is the second
				// shutdown signal after the earlier SIGINT; the apiserver's
				// SetupSignalContext handler responds to a second signal
				// with os.Exit(1), forcing immediate exit. The daemon also
				// caps its own drain at 45s before returning from Run, so
				// timeout values shorter than stopWaitTimeout (5m) routinely
				// race a still-draining daemon.
				_ = process.Kill(pid)
				err := ctx.Err()
				if errors.Is(err, context.DeadlineExceeded) {
					return fmt.Errorf("timed out waiting for %q control plane with pid %d to stop: %w", instance.Name(), pid, err)
				}
				return fmt.Errorf("wait for %q control plane with pid %d to stop cancelled: %w", instance.Name(), pid, err)
			case <-ticker.C:
				if !Running() {
					_ = os.Remove(instance.Config()) // Ignore error as file might not exist
					return nil
				}
			}
		}
	}

	// --wait=false: the daemon may still be serving for tens of seconds.
	// Leave instance.Config() alone so concurrent `rdd ctl`/`rdd kubectl`
	// calls keep working; the next `Start` rewrites it.
	return nil
}

// Delete removes all instance data. Callers must verify Exists() first; Delete
// assumes the instance exists. If the service is running, Delete waits up to
// timeout for it to exit before removing files; removing instance.Dir() while
// the serve process holds rdd.pid, db/state.db, and log files corrupts the
// directory on Windows and breaks PID-file mutual exclusion on Unix.
// Pass timeout 0 to wait indefinitely (bounded only by ctx).
func Delete(ctx context.Context, timeout time.Duration) error {
	if Running() {
		if err := StopWithWait(ctx, true, timeout); err != nil {
			// Return the error whenever the process is alive or the
			// deadline expired, so deletion never races a live daemon
			// and the CLI exits 4 on timeout per docs/design/cmd_service.md.
			// The predicate also absorbs signal-delivery failures and
			// context cancellation when the process has already exited;
			// the invariant is "the directory survives unless the daemon
			// is confirmed gone." context.Canceled is unreachable today
			// because the CLI runs on context.Background(); wiring SIGINT
			// into cmd.Context() would let Ctrl-C fall through to deletion
			// during a live stop, and needs an explicit decision here.
			if errors.Is(err, context.DeadlineExceeded) || Running() {
				return err
			}
		}
	}

	// Probe the datastore lock before anything destructive. state.db is the file a
	// still-running control plane holds open; remove it alone first, so a lock
	// error aborts the delete before os.RemoveAll could strip the -wal/-shm
	// sidecars or rdd.pid out from under a live daemon and orphan it. Once state.db
	// is gone the daemon is confirmed stopped, so clearing the rest is safe.
	dbDir := filepath.Join(instance.Dir(), "db")
	if err := os.Remove(filepath.Join(dbDir, "state.db")); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove datastore for %q; the control plane may still be running: %w", instance.Name(), err)
	}
	_ = os.RemoveAll(dbDir)

	preserveAllInstanceLogs()
	if os.Getenv("RDD_KEEP_LOGS") == "" {
		_ = os.RemoveAll(instance.LogDir())
	}
	// Unregister each WSL2 distro before dropping its directory: removing the
	// directory deletes the distro's ext4.vhdx but orphans the `wsl --list`
	// registration, which the next `rdd svc create` would boot against a
	// missing disk.
	wsl.UnregisterInstanceDistros(ctx, instance.LimaHome())
	_ = os.RemoveAll(instance.ShortDir())
	return os.RemoveAll(instance.Dir())
}

// preserveAllInstanceLogs moves .log files from each Lima instance directory
// to the service log directory before the instance directories are deleted.
// This is a no-op unless RDD_KEEP_LOGS is set.
//
// Errors are logged but do not prevent deletion. On Windows, os.Rename
// requires FILE_SHARE_DELETE on the source; Go sets this flag since 1.14,
// but non-Go processes (e.g., QEMU) may not. If rename fails because a
// process still holds a lock, the logs are lost when the instance directory
// is deleted afterward.
func preserveAllInstanceLogs() {
	if os.Getenv("RDD_KEEP_LOGS") == "" {
		return
	}
	entries, err := os.ReadDir(instance.LimaHome())
	if err != nil {
		if !os.IsNotExist(err) {
			logrus.WithError(err).Warn("Failed to read Lima instance directory")
		}
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		instDir := filepath.Join(instance.LimaHome(), entry.Name())
		count, err := instance.PreserveLogs(instDir, entry.Name())
		if err != nil {
			logrus.WithError(err).WithField("instance", entry.Name()).Warn("Failed to preserve instance logs")
			continue
		}
		if count > 0 {
			logrus.WithFields(logrus.Fields{"instance": entry.Name(), "count": count}).Debug("Preserved instance logs")
		}
	}
}

// ServeArgs returns the saved command-line arguments written by Create.
func ServeArgs() []string {
	data, err := os.ReadFile(instance.ArgsFile())
	if err == nil {
		var args []string
		if err := json.Unmarshal(data, &args); err == nil {
			return args
		}
	}
	return nil
}

// shouldEnableController determines if a controller should be enabled based on the controller's specification.
func shouldEnableController(controller base.Controller, spec string) bool {
	if controller.GetAPIGroup() == "builtin" {
		return true
	}
	// Empty spec: only builtin controllers are enabled
	if spec == "" {
		return false
	}

	controllerName := controller.GetName()
	apiGroup := controller.GetAPIGroup()

	var included bool
	var excluded bool

	parts := strings.Split(spec, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		isExclusion := strings.HasPrefix(part, "-")
		if isExclusion {
			part = strings.TrimPrefix(part, "-")
		}

		// Check for wildcard
		if part == "*" {
			if isExclusion {
				excluded = true
			} else {
				included = true
			}
			continue
		}

		// Check if it matches the API group
		if part == apiGroup {
			if isExclusion {
				excluded = true
			} else {
				included = true
			}
			continue
		}

		// Check if it matches the specific controller
		if part == controllerName {
			if isExclusion {
				excluded = true
			} else {
				included = true
			}
			continue
		}
	}

	// Return true if included and not excluded
	return included && !excluded
}

// NewServeCommand creates a *cobra.Command object with default parameters.
func NewServeCommand(ctx context.Context) *cobra.Command {
	s := options.NewOptions(ctx, instance.Dir())

	command := &cobra.Command{
		Use:          "serve",
		Long:         "The RDD controlplane is the backend service for Rancher Desktop 2",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			// silence client-go warnings.
			// kube-apiserver loopback clients should not log self-issued warnings.
			rest.SetDefaultWarningHandler(rest.NoWarnings{})
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			klog.Infof("os.Args: %v", os.Args)
			if Running() {
				return fmt.Errorf("control plane %q is already running", instance.Name())
			}
			if !Exists() {
				if err := Create(nil); err != nil {
					return err
				}
			}

			// Register the interrupt event before writing the PID file so a peer can
			// confirm our identity (`process.IsOurProcess`) as soon as the file exists.
			// On failure, bail before the PID file exists rather than run a daemon that
			// `rdd svc stop` cannot find. The daemon's shutdown runs when the event
			// fires; no-op on Unix.
			ctx := genericapiserver.SetupSignalContext()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			releaseInterrupt, err := process.RegisterInterruptHandler(process.ServeInterruptKey, cancel)
			if err != nil {
				return fmt.Errorf("failed to register interrupt handler: %w", err)
			}
			defer releaseInterrupt()

			pid := []byte(strconv.Itoa(os.Getpid()))
			if err := os.WriteFile(instance.PIDFile(), pid, 0o600); err != nil {
				return fmt.Errorf("failed to write %q: %w", instance.PIDFile(), err)
			}

			verflag.PrintAndExitIfRequested()
			fs := cmd.Flags()

			// Activate logging as soon as possible, after that
			// show flags with the final logging configuration.
			if err := logsapi.ValidateAndApply(s.ControlPlane.Logs, utilfeature.DefaultFeatureGate); err != nil {
				return err
			}
			cliflag.PrintFlags(fs)

			// Publish the application's bundled binaries to the instance bin
			// directory. Best-effort: a failure must not block the control plane.
			if err := binlinks.LinkBundledBinaries(); err != nil {
				klog.Warningf("Failed to link bundled binaries: %v", err)
			}

			completedOptions, err := s.Complete(cmd.Context())
			if err != nil {
				return err
			}

			if errs := completedOptions.Validate(); len(errs) != 0 {
				return kerrors.NewAggregate(errs)
			}

			// add feature enablement metrics
			utilfeature.DefaultMutableFeatureGate.AddMetrics()

			// change into instance dir because kine will create the db relative to the current dir
			if err := os.Chdir(instance.Dir()); err != nil {
				return fmt.Errorf("cannot chdir to %q: %w", instance.Dir(), err)
			}
			return Run(ctx, *completedOptions)
		},
	}

	var namedFlagSets cliflag.NamedFlagSets
	s.AddFlags(&namedFlagSets)
	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), command.Name())

	fs := command.Flags()
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	cols, _, _ := term.TerminalSize(command.OutOrStdout())
	cliflag.SetUsageAndHelpFunc(command, namedFlagSets, cols)

	startOptionsCmd := &cobra.Command{
		Use:   "options",
		Short: "Show all start command options",
		Long: help.Doc(`
			Show all start command options

			"rdd start"" has a large number of options. This command shows all of them.
		`),
		PersistentPreRunE: func(*cobra.Command, []string) error {
			// silence client-go warnings.
			// apiserver loopback clients should not log self-issued warnings.
			rest.SetDefaultWarningHandler(rest.NoWarnings{})
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, _ = fmt.Fprintf(cmd.OutOrStderr(), usageFmt, command.UseLine())
			cliflag.PrintSections(cmd.OutOrStderr(), namedFlagSets, cols)
			return nil
		},
	}
	command.AddCommand(startOptionsCmd)

	setPartialUsageAndHelpFunc(command, namedFlagSets, cols, []string{
		"etcd-servers",
	})

	help.FitTerminal(command.OutOrStdout())

	return command
}

// Run runs the specified APIServer. This should never exit.
func Run(ctx context.Context, opts options.CompletedOptions) error {
	klog.Infof("Version: %+v", version.Get())
	klog.InfoS("Golang settings", "GOGC", os.Getenv("GOGC"), "GOMAXPROCS", os.Getenv("GOMAXPROCS"), "GOTRACEBACK", os.Getenv("GOTRACEBACK"))

	// Start kine before NewConfig because BuildGenericConfig opens gRPC
	// connections to the etcd backend immediately. Kine uses a child context
	// cancelled after the controller manager shuts down, so the etcd client
	// connections drain before the socket disappears.
	var kineCancel context.CancelFunc
	if opts.Datastore.Enabled {
		dsConfig, err := datastore.NewConfig(opts.Datastore)
		if err != nil {
			return err
		}
		var kineCtx context.Context
		kineCtx, kineCancel = context.WithCancel(ctx)
		defer kineCancel()
		klog.Infof("Starting kine/sqlite server listening on %s", opts.Datastore.EndpointConfig.Listener)
		if err := datastore.NewServer(dsConfig.Complete()).Run(kineCtx); err != nil {
			return fmt.Errorf("failed to start kine server: %w", err)
		}
	}

	config, err := options.NewConfig(opts)
	if err != nil {
		return err
	}
	completed, err := config.Complete()
	if err != nil {
		return err
	}

	server, err := createServerChain(completed)
	if err != nil {
		return err
	}

	prepared, err := server.PrepareRun()
	if err != nil {
		return err
	}

	externalCACert, _ := completed.ControlPlane.Generic.SecureServing.Cert.CurrentCertKeyContent()

	// Use the actual bound port from the secure serving configuration
	// Force kubeconfig to use localhost to avoid conflicts with Rancher Desktop
	_, actualPort, err := completed.ControlPlane.Generic.SecureServing.HostPort()
	if err != nil {
		return fmt.Errorf("failed to get actual bound port: %w", err)
	}
	externalKubeConfigHost := fmt.Sprintf("https://127.0.0.1:%d", actualPort)

	if err := storeKubeConfigToDisk(
		completed.ExtraConfig.AdminToken,
		completed.ExtraConfig.UserToken,
		externalKubeConfigHost,
		"", // TLSServerName
		externalCACert,
	); err != nil {
		klog.Warningf("Failed to store kubeconfig to disk: %v", err)
		return err
	}

	// Run the server and wait for readiness
	go func() {
		if err := prepared.Run(ctx); err != nil {
			klog.Fatal(err, "Failed to run server")
		}
	}()

	klog.Info("Waiting for control plane to be ready")

	restConfig, err := GetKubeRestConfig()
	if err != nil {
		return err
	}
	if err := readiness.WaitForReady(ctx, restConfig, true); err != nil {
		return err
	}

	// Create the discovery ConfigMap before any controller managers register.
	// Its creationTimestamp serves as the control plane start time.
	initClient, err := kubernetes.NewForConfig(completed.ControlPlane.Generic.LoopbackClientConfig)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client for discovery init: %w", err)
	}
	if err := controllers.InitDiscovery(ctx, initClient); err != nil {
		return fmt.Errorf("failed to initialize discovery: %w", err)
	}

	// Start shared controller manager for enabled controllers
	var enabledControllers []base.Controller

	// Get all registered controllers from the registry
	allControllers := base.GetAllControllers()
	controllersSpec := completed.Controllers.Controllers

	// Enable controllers: builtin controllers are always enabled, others based on --controllers flag
	for _, controller := range allControllers {
		if shouldEnableController(controller, controllersSpec) {
			enabledControllers = append(enabledControllers, controller)
		}
	}

	// Start shared controller manager if any controllers are enabled.
	// The WaitGroup ensures the controller manager completes shutdown
	// (including terminating hostagent processes) before Run returns.
	var mgrWg sync.WaitGroup
	if len(enabledControllers) == 0 {
		// No controllers means no CRDs to install, so signal
		// readiness immediately for waiting clients.
		if err := controllers.MarkControlPlaneReady(ctx, initClient); err != nil {
			return fmt.Errorf("failed to mark control plane as ready: %w", err)
		}
	} else {
		mgrWg.Add(1)
		go func() {
			defer mgrWg.Done()
			klog.InfoS("Starting shared controller manager", "controllers", len(enabledControllers))

			// Each instance reserves 4 consecutive ports starting at 8080:
			// +0 external metrics, +1 external health, +2 embedded metrics, +3 embedded health.
			// Start() resolves these to available ports immediately before binding.
			instanceOffset := 4 * instance.Index()
			metricsPort := 8082 + instanceOffset
			healthPort := 8083 + instanceOffset

			// Create shared controller manager
			sharedManager, err := controllers.NewSharedControllerManager(
				"embedded",
				completed.ControlPlane.Generic.LoopbackClientConfig,
				metricsPort,
				healthPort,
			)
			if err != nil {
				klog.Error(err, "Failed to create shared controller manager")
				return
			}

			// Register all enabled controllers
			for _, controller := range enabledControllers {
				if err := sharedManager.RegisterController(controller); err != nil {
					klog.Error(err, "Failed to register controller", "controller", controller.GetName())
					return
				}
			}

			// Start the shared manager (this blocks until context is cancelled)
			if err := sharedManager.Start(ctx); err != nil {
				klog.Error(err, "Failed to start shared controller manager")
			}
		}()
	}

	<-ctx.Done()

	// Wait for the controller manager to finish shutdown, but don't block
	// forever — a misbehaving controller should not prevent the service
	// from exiting. The graceful shutdown timeout for hostagents is 30s;
	// allow 45s total before giving up.
	mgrDone := make(chan struct{})
	go func() {
		mgrWg.Wait()
		close(mgrDone)
	}()
	select {
	case <-mgrDone:
	case <-time.After(45 * time.Second):
		klog.Warning("Controller manager did not shut down within 45s, exiting anyway")
	}

	return nil
}

// createServerChain creates the apiserver instances connected via delegation.
func createServerChain(config options.CompletedConfig) (*aggregatorapiserver.APIAggregator, error) {
	// Basic not found handler
	notFoundHandler := notfoundhandler.New(config.ControlPlane.Generic.Serializer, genericapifilters.NoMuxAndDiscoveryIncompleteKey)

	// Mux exists so we can set up [base.PassthroughController] controllers later
	// if needed; see the documentation for that interface.
	mux := http.NewServeMux()
	mux.Handle("/", notFoundHandler)

	var aggregatorServer *aggregatorapiserver.APIAggregator
	var apiExtensionsServer *apiextensionapiserver.CustomResourceDefinitions
	var nativeAPIs *controlplaneapiserver.Server
	var err error

	// CRDs are always enabled - create extension server
	apiExtensionsServer, err = config.APIExtensions.New(genericapiserver.NewEmptyDelegateWithCustomHandler(mux))
	if err != nil {
		return nil, fmt.Errorf("failed to create apiextensions-apiserver: %w", err)
	}

	nativeAPIs, err = config.ControlPlane.New("rdd-controlplane", apiExtensionsServer.GenericAPIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create RDD controlplane apiserver: %w", err)
	}

	client, err := kubernetes.NewForConfig(config.ControlPlane.Generic.LoopbackClientConfig)
	if err != nil {
		return nil, err
	}
	storageProviders, err := config.ControlPlane.GenericStorageProviders(client.Discovery())
	if err != nil {
		return nil, fmt.Errorf("failed to create storage providers: %w", err)
	}

	// Filter to only required API groups
	var filteredProviders []controlplaneapiserver.RESTStorageProvider
	for _, provider := range storageProviders {
		// Only include required API groups
		if requiredAPIGroups.Has(provider.GroupName()) || provider.GroupName() == "" {
			filteredProviders = append(filteredProviders, provider)
		}
	}
	storageProviders = filteredProviders

	if err := nativeAPIs.InstallAPIs(storageProviders...); err != nil {
		return nil, fmt.Errorf("failed to install APIs: %w", err)
	}
	for _, storageProvider := range storageProviders {
		klog.Infof("Serving %s", storageProvider.GroupName())
	}

	// Aggregator for APIServices, discovery and OpenAPI
	// CRDs are always enabled - wire in aggregator server
	aggregatorServer, err = controlplaneapiserver.CreateAggregatorServer(config.Aggregator, nativeAPIs.GenericAPIServer, apiExtensionsServer.Informers.Apiextensions().V1().CustomResourceDefinitions(), false, controlplaneapiserver.DefaultGenericAPIServicePriorities())
	if err != nil {
		// we don't need special handling for innerStopCh because the aggregator server doesn't create any go routines
		return nil, fmt.Errorf("failed to create kube-aggregator: %w", err)
	}

	// Set up the passthrough handler
	discovery, err := controllers.NewControllerManagerDiscovery(config.ControlPlane.Generic.LoopbackClientConfig)
	if err != nil {
		return nil, fmt.Errorf("could not create discovery client: %w", err)
	}
	mux.Handle("/passthrough/", http.StripPrefix("/passthrough", controllers.NewPassthroughHandler(discovery)))

	return aggregatorServer, nil
}
