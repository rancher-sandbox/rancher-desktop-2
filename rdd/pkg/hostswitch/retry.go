// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package hostswitch

import (
	"context"
	"time"

	"github.com/go-logr/logr"
)

// runLoop supervises attempt, restarting it whenever it exits while ctx is
// still live. It returns nil once ctx is cancelled (the hostagent is shutting
// down; the OS reclaims host-switch resources when the process exits).
//
// Restarts are rate-limited to one per interval, measured from the start of
// each attempt, so a long-lived bridge restarts immediately while a crash loop
// backs off. An attempt returning nil without cancellation is not expected (a
// clean stop implies ctx cancellation, caught first), but is guarded against a
// hot loop by the same rate limit.
//
// The loop is the only new logic host-switch adds and is Windows-only in
// production; keeping it here, decoupled from runOnce and the concrete
// interval, lets it be exercised on every platform.
func runLoop(ctx context.Context, logger logr.Logger, interval time.Duration, attempt func(context.Context) error) error {
	for {
		start := time.Now()
		err := attempt(ctx)
		if ctx.Err() != nil {
			logger.Info("Host-switch stopped")
			return nil
		}
		if err == nil {
			logger.Info("Host-switch stopped without cancellation; restarting")
		} else {
			logger.Error(err, "Host-switch exited unexpectedly; guest networking is down until it restarts")
		}
		if remaining := interval - time.Since(start); remaining > 0 {
			select {
			case <-ctx.Done():
				logger.Info("Host-switch stopped")
				return nil
			case <-time.After(remaining):
			}
		}
	}
}
