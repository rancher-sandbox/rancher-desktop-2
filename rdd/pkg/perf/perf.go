// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package perf records phase timings, which say how long rdd spent in each
// step of starting the control plane, creating and booting a VM, and
// bringing up the App.
//
// Every record is a V(0) "Phase timing" line through the context's logger,
// so a support bundle shows where a slow start spent its time. A context
// without a logger, such as the CLI's, doesn't log anything. When the
// RDD_PERF_LOG environment variable names a file, the record is also
// appended there as one line in the format below. The path must be
// absolute. The daemon changes into its instance directory before it
// records anything, but the CLI records from its own working directory, so
// a relative path would split the records between two files.
//
//	<unix_ms> metric=<name> value=<number> unit=<unit> <label>=<value>...
//
// A value that is empty, or that contains a space, a quote, "=" or a
// character that strconv.Quote escapes, is quoted as a Go string.
//
// Each metric has a fixed set of labels, so the number of series stays
// bounded. Tools that parse the records rely on the list below.
//
//	cp.init                     ms  process start until the control plane starts; includes writing the PID file, setting up logging, linking the bundled binaries and completing the options
//	cp.kine                     ms  starting the kine datastore
//	cp.apiserver_ready          ms  creating the apiserver until it reports ready
//	cp.discovery                ms  creating the discovery ConfigMap
//	cp.crds                     ms  installing CRDs and registering controllers
//	cp.webhooks                 ms  creating the webhook configurations
//	cp.ready                    ms  starting the manager until the control plane is marked ready
//	cp.total                    ms  process start until the control plane is marked ready
//	svc.start                   ms  "rdd svc start", or a command that starts the service on demand, until the control plane is ready (500 ms poll)
//	svc.stop                    ms  "rdd svc stop" or "rdd svc delete" until the process has exited (100 ms poll); not recorded with --wait=false
//	limavm.create.decompress    ms  decompressing the embedded distro image; labels vm, vmtype
//	limavm.create.prepare       ms  Lima's Prepare: image download or conversion, and disk creation; labels vm, vmtype
//	limavm.create.total         ms  creating the Lima instance until it is marked Created; labels vm, vmtype
//	limavm.boot.driver          ms  hostagent start until its first event; labels vm, vmtype, first
//	limavm.boot.req             ms  one boot requirement, pending until done or failed; labels vm, vmtype, first, req, result
//	limavm.boot.total           ms  hostagent start until it reports running; labels vm, vmtype, first
//	limavm.stop                 ms  stopping the hostagent; labels vm, mode (graceful or forced)
//	limavm.delete               ms  deleting the LimaVM: stop, instance removal and finalizer; labels vm; not recorded when the Lima instance was already gone
//	cond.flip                   ms  last generation change, or rdd first seeing the object, until a condition changes status or reason; labels kind, cond, status, reason
//	cond.after_running          ms  App Running turning True until ContainerEngineReady or KubernetesReady turns True; labels cond
//
// The "first" label of the boot records is true only for the first boot
// after this process created the instance.
//
// Only the process that started the control plane records the cp.* and
// cond.* metrics.
package perf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

// LogEnv names the environment variable that holds the file records are
// appended to.
const LogEnv = "RDD_PERF_LOG"

// UnitMS is the unit of every duration record.
const UnitMS = "ms"

// processStart approximates the process start time. Package variables are
// initialized before main runs.
var processStart = time.Now()

// fileMu serializes appends from this process, so each record stays one line.
var fileMu sync.Mutex

// ProcessStart returns when this process started, as closely as the
// package can observe it.
func ProcessStart() time.Time {
	return processStart
}

// Record logs one measurement and, when RDD_PERF_LOG is set, appends it to
// that file. Labels are key/value pairs; a trailing key without a value gets
// an empty value.
func Record(ctx context.Context, metric string, value int64, unit string, labels ...string) {
	logger := logr.FromContextOrDiscard(ctx)
	keysAndValues := []any{"metric", metric, "value", value, "unit", unit}
	for i := 0; i < len(labels); i += 2 {
		keysAndValues = append(keysAndValues, labels[i], labelValue(labels, i))
	}
	logger.Info("Phase timing", keysAndValues...)

	path := os.Getenv(LogEnv)
	if path == "" {
		return
	}
	line := formatLine(time.Now(), metric, value, unit, labels)
	if err := appendLine(path, line); err != nil {
		logger.Error(err, "Failed to append phase timing", "path", path)
	}
}

// Duration records d in milliseconds.
func Duration(ctx context.Context, metric string, d time.Duration, labels ...string) {
	Record(ctx, metric, d.Milliseconds(), UnitMS, labels...)
}

// Since records the milliseconds elapsed since start.
func Since(ctx context.Context, metric string, start time.Time, labels ...string) {
	Duration(ctx, metric, time.Since(start), labels...)
}

// Clock times consecutive phases. Each Mark records the time since the
// previous Mark, or since ProcessStart for the first Mark. A nil Clock
// doesn't record anything, so code shared with processes that do not time
// their phases can call Mark unconditionally. A Clock is not safe for
// concurrent use.
type Clock struct {
	last time.Time
}

// NewClock returns a Clock whose first phase starts at ProcessStart.
func NewClock() *Clock {
	return &Clock{last: processStart}
}

// Mark records the phase that ends now.
func (c *Clock) Mark(ctx context.Context, metric string, labels ...string) {
	if c == nil {
		return
	}
	Duration(ctx, metric, c.lap(time.Now()), labels...)
}

// lap returns the time from the previous lap to now, and starts the next
// phase at now.
func (c *Clock) lap(now time.Time) time.Duration {
	d := now.Sub(c.last)
	c.last = now
	return d
}

type clockKey struct{}

// WithClock returns a context that carries c.
func WithClock(ctx context.Context, c *Clock) context.Context {
	return context.WithValue(ctx, clockKey{}, c)
}

// ClockFrom returns the Clock that ctx carries, or nil.
func ClockFrom(ctx context.Context) *Clock {
	c, _ := ctx.Value(clockKey{}).(*Clock)
	return c
}

func labelValue(labels []string, i int) string {
	if i+1 < len(labels) {
		return labels[i+1]
	}
	return ""
}

// formatLine encodes one record as a logfmt line ending in a newline.
func formatLine(now time.Time, metric string, value int64, unit string, labels []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d metric=%s value=%d unit=%s", now.UnixMilli(), quote(metric), value, quote(unit))
	for i := 0; i < len(labels); i += 2 {
		fmt.Fprintf(&b, " %s=%s", labels[i], quote(labelValue(labels, i)))
	}
	b.WriteByte('\n')
	return b.String()
}

// quote returns s, quoted as a Go string if s is empty, or if it contains a
// space, a quote, "=" or a character that strconv.Quote escapes.
func quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \"=") || strconv.Quote(s) != `"`+s+`"` {
		return strconv.Quote(s)
	}
	return s
}

// appendLine appends line to the file at path with a single write, creating
// the file when needed. Opening the file for each record doesn't keep a
// descriptor open, and it lets several processes append to the same file.
func appendLine(path, line string) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(line)
	return errors.Join(err, f.Close())
}
