// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The Lima Authors

// Package hostagent provides the hostagent command for running Lima's hostagent.
package hostagent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"

	"github.com/lima-vm/lima/v2/pkg/hostagent"
	"github.com/lima-vm/lima/v2/pkg/hostagent/api/server"
	"github.com/lima-vm/lima/v2/pkg/store"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestagent"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/process"
)

// NewCommand creates a new hostagent cobra command.
func NewCommand() *cobra.Command {
	hostagentCommand := &cobra.Command{
		Use:    "hostagent INSTANCE",
		Short:  "Run hostagent",
		Args:   cobra.ExactArgs(1),
		RunE:   hostagentAction,
		Hidden: true,
	}
	hostagentCommand.Flags().StringP("pidfile", "p", "", "Write PID to file")
	hostagentCommand.Flags().String("socket", "", "Path of hostagent socket")
	hostagentCommand.Flags().Bool("run-gui", false, "Run GUI synchronously within hostagent")
	hostagentCommand.Flags().String("nerdctl-archive", "", "Local file path of nerdctl archive")
	hostagentCommand.Flags().Bool("progress", false, "Show provision script progress")
	hostagentCommand.Flags().Bool("debug", false, "Enable debug logging")
	return hostagentCommand
}

func hostagentAction(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	pidfile, err := cmd.Flags().GetString("pidfile")
	if err != nil {
		return err
	}
	instName := args[0]

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM)

	// Register the interrupt event before writing the PID file so a peer can
	// confirm our identity (IsOurProcess) as soon as the file exists. On failure,
	// return before the PID file exists rather than run a hostagent the control
	// plane cannot signal; the lima reconciler retries the launch with backoff.
	// The closure forwards the event as os.Interrupt; no-op on Unix.
	releaseInterrupt, err := process.RegisterInterruptHandler(process.HostagentInterruptKey(instName), func() {
		select {
		case signalCh <- os.Interrupt:
		default:
		}
	})
	if err != nil {
		return fmt.Errorf("failed to register interrupt handler: %w", err)
	}
	defer releaseInterrupt()

	if pidfile != "" {
		if existingPID, err := store.ReadPIDFile(pidfile); existingPID != 0 {
			return fmt.Errorf("another hostagent may already be running with pid %d (pidfile %q)", existingPID, pidfile)
		} else if err != nil {
			return fmt.Errorf("failed to determine if another hostagent is running: %w", err)
		}
		if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
			return err
		}
		defer os.RemoveAll(pidfile)
	}
	socket, err := cmd.Flags().GetString("socket")
	if err != nil {
		return err
	}
	if socket == "" {
		return errors.New("socket must be specified")
	}

	runGUI, err := cmd.Flags().GetBool("run-gui")
	if err != nil {
		return err
	}
	if runGUI {
		// Without this the call to vz.RunGUI fails. Adding it here, as this has to be called before the vz cgo loads.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	debug, _ := cmd.Flags().GetBool("debug")
	if debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	stdout := &syncWriter{w: cmd.OutOrStdout()}
	stderr := &syncWriter{w: cmd.ErrOrStderr()}

	initHostagentLogrus(stderr)
	// Extract the embedded guest agent binary for VM provisioning.
	gaPath, gaCleanup, err := guestagent.WriteTempFile()
	if err != nil {
		return fmt.Errorf("extracting embedded guest agent: %w", err)
	}
	defer gaCleanup()

	var opts []hostagent.Opt
	opts = append(opts, hostagent.WithGuestAgentBinary(gaPath))
	nerdctlArchive, err := cmd.Flags().GetString("nerdctl-archive")
	if err != nil {
		return err
	}
	if nerdctlArchive != "" {
		opts = append(opts, hostagent.WithNerdctlArchive(nerdctlArchive))
	}
	showProgress, err := cmd.Flags().GetBool("progress")
	if err != nil {
		return err
	}
	if showProgress {
		opts = append(opts, hostagent.WithCloudInitProgress(showProgress))
	}
	ha, err := hostagent.New(ctx, instName, stdout, signalCh, opts...)
	if err != nil {
		return err
	}

	backend := &server.Backend{
		Agent: ha,
	}
	r := http.NewServeMux()
	server.AddRoutes(r, backend)
	srv := &http.Server{Handler: r}
	if err := os.RemoveAll(socket); err != nil {
		return err
	}
	var lc net.ListenConfig
	l, err := lc.Listen(ctx, "unix", socket)
	if err != nil {
		return err
	}
	logrus.Infof("hostagent socket created at %s", socket)
	go func() {
		if serveErr := srv.Serve(l); serveErr != http.ErrServerClosed {
			logrus.WithError(serveErr).Warn("hostagent API server exited with an error")
		}
	}()
	defer srv.Close()
	return ha.Run(cmd.Context())
}

// syncer is implemented by *os.File.
type syncer interface {
	Sync() error
}

type syncWriter struct {
	w io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	written, err := w.w.Write(p)
	if err == nil {
		if s, ok := w.w.(syncer); ok {
			_ = s.Sync()
		}
	}
	return written, err
}

func initHostagentLogrus(stderr io.Writer) {
	logrus.SetOutput(stderr)
	// JSON logs are parsed in pkg/hostagent/events.Watcher()
	logrus.SetFormatter(new(logrus.JSONFormatter))
	// HostAgent logging is one level more verbose than the start command itself
	if logrus.GetLevel() == logrus.DebugLevel {
		logrus.SetLevel(logrus.TraceLevel)
	} else {
		logrus.SetLevel(logrus.DebugLevel)
	}
}
