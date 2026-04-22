// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
package tail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"gotest.tools/v3/assert"
)

func TestStream(t *testing.T) {
	const expected = "This is the last line of text"

	testCases := []struct {
		name    string
		follow  bool
		prepare func(w io.WriteCloser) error
		finish  func(w io.WriteCloser) error
	}{
		{
			name:   "do not follow",
			follow: false,
			prepare: func(w io.WriteCloser) error {
				for i := range 1000 {
					_, err := fmt.Fprintf(w, "This is line %d with some length\n", i)
					if err != nil {
						return err
					}
				}
				_, err := fmt.Fprintln(w, expected)
				return err
			},
			finish: func(w io.WriteCloser) error {
				return w.Close()
			},
		},
		{
			name:   "follow",
			follow: true,
			prepare: func(w io.WriteCloser) error {
				for i := range 100 {
					_, err := fmt.Fprintf(w, "This is line %d in the first block\n", i)
					if err != nil {
						return err
					}
				}
				return nil
			},
			finish: func(w io.WriteCloser) error {
				defer w.Close()
				for i := range 100 {
					_, err := fmt.Fprintf(w, "This is line %d in the second block\n", i)
					if err != nil {
						return err
					}
				}
				_, err := fmt.Fprintln(w, expected)
				return err
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := filepath.Join(t.TempDir(), "output.log")
			f, err := os.Create(n)
			assert.NilError(t, err, "failed to create test file")
			defer f.Close()
			r, w := io.Pipe()

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			// Across a bunch of goroutines, we need to:
			// - Set up the scanner
			// - Run the `prepare` function
			// - Run File until it gets stuck
			// - Run the `finish` function
			// - Run File until it gets stuck (again)
			// - End the File context (so it ends)
			// - Check that the last line was the expected output

			lastLine := "initial value"
			lines := make(chan string)
			scanner := bufio.NewScanner(r)
			go func() {
				defer close(lines)
				for scanner.Scan() {
					lines <- scanner.Text()
				}
			}()
			// waitForStuck will run until there has been half a second without
			// any lines being emitted.
			waitForStuck := func() {
				for {
					select {
					case lastLine = <-lines:
					case <-time.After(100 * time.Millisecond):
						return
					}
				}
			}

			tailCtx, done := context.WithCancel(ctx)
			assert.NilError(t, tc.prepare(f))
			wg, tailCtx := errgroup.WithContext(tailCtx)
			wg.Go(func() error {
				return Stream(tailCtx, w, n, tc.follow)
			})
			waitForStuck()
			assert.NilError(t, tc.finish(f))
			waitForStuck()
			done()

			// Close the writer, so the scanner knows we're done.
			assert.NilError(t, w.Close())
			assert.NilError(t, wg.Wait(), "failed to wait for cleanup")
			assert.Equal(t, expected, lastLine)
		})
	}
}

// failingWriter returns err after failAfter successful writes. Used
// to exercise Stream's mid-stream writer-error path.
type failingWriter struct {
	mu        sync.Mutex
	calls     int
	failAfter int
	err       error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls > w.failAfter {
		return 0, w.err
	}
	return len(p), nil
}

// TestStreamReturnsWriterError covers stream.go's writer-error path.
// When the destination writer fails mid-stream, Stream captures the
// error, kills the tomb, drains Lines so tailFileSync exits cleanly,
// and returns the writer's error.
func TestStreamReturnsWriterError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.log")
	f, err := os.Create(path)
	assert.NilError(t, err)
	for i := range 100 {
		_, err := fmt.Fprintf(f, "line %d\n", i)
		assert.NilError(t, err)
	}
	assert.NilError(t, f.Close())

	w := &failingWriter{failAfter: 1, err: io.ErrClosedPipe}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err = Stream(ctx, w, path, false)
	assert.Assert(t, errors.Is(err, io.ErrClosedPipe), "expected ErrClosedPipe, got %v", err)
}
