// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The Lima Authors

// Package events parses the JSON event stream written by Lima's hostagent
// to its stdout log. It is a small, local replacement for
// github.com/lima-vm/lima/v2/pkg/hostagent/events.Watch that uses our own
// forked tail library (pkg/util/tail) to avoid the Windows deadlock in
// the upstream nxadm/tail shared InotifyTracker.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

// Status mirrors the subset of lima's hostagent events.Status that the
// rdd LimaVM controller consumes. Fields are kept in sync with the JSON
// schema emitted by Lima's hostagent so raw JSON lines unmarshal cleanly.
type Status struct {
	// Running reports that the hostagent finished booting the VM. When
	// true, Exiting is false.
	Running bool `json:"running,omitempty"`

	// Degraded reports that the hostagent considers the VM running but
	// in a degraded state. When true, Running must also be true.
	Degraded bool `json:"degraded,omitempty"`

	// Exiting reports that the hostagent is shutting down the VM. When
	// true, Running is false.
	Exiting bool `json:"exiting,omitempty"`

	// Errors is a list of any errors reported alongside the event.
	Errors []string `json:"errors,omitempty"`

	// SSHLocalPort is the port on 127.0.0.1 that sshd on the guest is
	// reachable on. A non-zero value indicates that the hostagent has
	// finished network setup.
	SSHLocalPort int `json:"sshLocalPort,omitempty"`
}

// Event is a single JSON line emitted by the hostagent.
type Event struct {
	Time   time.Time `json:"time,omitempty"`
	Status Status    `json:"status,omitempty"`
}

// Watch tails the hostagent stdout and stderr logs, decodes each stdout
// line as an Event, and invokes onEvent for each one that occurred at
// or after begin. It returns when onEvent returns true, when ctx is
// cancelled, or when either tail is terminated because the underlying
// file was deleted.
//
// Lines written before begin are skipped. Stderr lines are drained but
// not propagated; the upstream Lima signature had a propagateStderr
// flag that rdd never set, so it is not reproduced here.
//
// Diagnostics go through log.FromContext(ctx) so the caller's
// instance/component fields stay attached.
//
// Unlike Lima's events.Watch, this does NOT use the shared InotifyTracker
// in github.com/nxadm/tail. It uses the rdd fork at pkg/util/tail, whose
// tracker cannot deadlock when fsnotify reports an internal error.
func Watch(ctx context.Context, haStdoutPath, haStderrPath string, begin time.Time, onEvent func(Event) bool) error {
	logger := log.FromContext(ctx)
	cfg := tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Logger:    tailLogger{logger: logger},
	}

	haStdoutTail, err := tail.Open(haStdoutPath, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = haStdoutTail.Stop()
		// Do NOT call Cleanup; it unregisters the tracker entry in a way
		// that prevents the process from ever tailing the file again.
	}()

	haStderrTail, err := tail.Open(haStderrPath, cfg)
	if err != nil {
		return err
	}
	defer func() {
		_ = haStderrTail.Stop()
		// Do NOT call Cleanup; it unregisters the tracker entry in a way
		// that prevents the process from ever tailing the file again.
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-haStdoutTail.Lines:
			if line == nil {
				return haStdoutTail.Err()
			}
			if line.Err != nil {
				logger.Error(line.Err, "Hostagent stdout tail error")
				continue
			}
			if line.Text == "" {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(line.Text), &ev); err != nil {
				logger.Error(err, "Ignoring unparseable hostagent stdout line", "line", line.Text)
				continue
			}
			logger.V(1).Info("Received a hostagent event", "event", ev)
			if !begin.IsZero() && ev.Time.Before(begin) {
				continue
			}
			if stop := onEvent(ev); stop {
				return nil
			}
		case line := <-haStderrTail.Lines:
			if line == nil {
				return haStderrTail.Err()
			}
			if line.Err != nil {
				logger.Error(line.Err, "Hostagent stderr tail error")
			}
		}
	}
}

// tailLogger adapts a logr.Logger to the subset of the stdlib log.Logger
// API that pkg/util/tail's Config.Logger accepts. In practice tail only
// calls Printf; the other methods exist to satisfy the interface and
// forward through Info so a stray call at least surfaces in the log.
type tailLogger struct {
	logger logr.Logger
}

func (t tailLogger) Fatal(v ...any)                 { t.logger.Info(fmt.Sprint(v...)) }
func (t tailLogger) Fatalf(format string, v ...any) { t.logger.Info(fmt.Sprintf(format, v...)) }
func (t tailLogger) Fatalln(v ...any)               { t.logger.Info(fmt.Sprintln(v...)) }
func (t tailLogger) Panic(v ...any)                 { t.logger.Info(fmt.Sprint(v...)) }
func (t tailLogger) Panicf(format string, v ...any) { t.logger.Info(fmt.Sprintf(format, v...)) }
func (t tailLogger) Panicln(v ...any)               { t.logger.Info(fmt.Sprintln(v...)) }
func (t tailLogger) Print(v ...any)                 { t.logger.Info(fmt.Sprint(v...)) }
func (t tailLogger) Printf(format string, v ...any) { t.logger.Info(fmt.Sprintf(format, v...)) }
func (t tailLogger) Println(v ...any)               { t.logger.Info(fmt.Sprintln(v...)) }
