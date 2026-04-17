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

	"github.com/sirupsen/logrus"

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
// Unlike Lima's events.Watch, this does NOT use the shared InotifyTracker
// in github.com/nxadm/tail. It uses the rdd fork at pkg/util/tail, whose
// tracker cannot deadlock when fsnotify reports an internal error.
func Watch(ctx context.Context, haStdoutPath, haStderrPath string, begin time.Time, onEvent func(Event) bool) error {
	cfg := tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: false,
		Logger:    logrus.StandardLogger(),
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
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-haStdoutTail.Lines:
			if line == nil {
				return nil
			}
			if line.Err != nil {
				logrus.WithError(line.Err).Error("hostagent stdout tail error")
				continue
			}
			if line.Text == "" {
				continue
			}
			var ev Event
			if err := json.Unmarshal([]byte(line.Text), &ev); err != nil {
				return fmt.Errorf("failed to unmarshal %q as %T: %w", line.Text, ev, err)
			}
			logrus.WithField("event", ev).Debug("received a hostagent event")
			if !begin.IsZero() && ev.Time.Before(begin) {
				continue
			}
			if stop := onEvent(ev); stop {
				return nil
			}
		case line := <-haStderrTail.Lines:
			if line == nil {
				return nil
			}
			if line.Err != nil {
				logrus.WithError(line.Err).Error("hostagent stderr tail error")
			}
		}
	}
}
