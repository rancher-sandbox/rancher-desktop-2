// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package tail

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Stream prints the contents of filePath to writer. If follow is true, it waits
// for new lines at EOF; otherwise it returns at EOF. The stream stops when ctx
// is cancelled, when the writer returns an error (e.g. broken pipe), or (when
// follow is false) when EOF is reached.
func Stream(ctx context.Context, writer io.Writer, filePath string, follow bool) error {
	config := Config{
		ReOpen:        follow,
		Follow:        follow,
		CompleteLines: true,
	}
	t, err := Open(filePath, config)
	if err != nil {
		return err
	}

	if !follow {
		// Signal that we want to stop tailing at EOF.
		go func() {
			_ = t.StopAtEOF()
		}()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = t.Stop()
		case <-done:
		}
	}()

	var writeErr error
	for line := range t.Lines {
		if writeErr != nil {
			continue // drain Lines so tailFileSync can exit cleanly
		}
		if _, err := fmt.Fprintln(writer, line.Text); err != nil {
			writeErr = err
			t.Kill(err) // non-blocking; keep draining Lines so tailFileSync can finish
		}
	}
	// Block until tailFileSync's full defer chain (including watchers.Wait)
	// has run, so a caller that re-opens the same file does not race with
	// an untrack still in flight on the shared InotifyTracker. Wait also
	// surfaces any fatal error the tail hit (e.g. a mid-stream read error
	// that would otherwise be silently dropped when Lines closed). The
	// follow=false path kills the tomb with errStopAtEOF on purpose, so
	// that sentinel is not a real failure.
	if err := t.Wait(); err != nil && writeErr == nil && !errors.Is(err, errStopAtEOF) {
		return err
	}
	return writeErr
}
