// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package xz decompresses xz streams in-process with a pure-Go decoder, so rdd
// can provision a VM image without a system xz binary on the host.
package xz

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	ulikunitz "github.com/ulikunitz/xz"
)

// bufSize is the input read buffer size. ulikunitz/xz issues many small reads;
// without a large buffer wrapping the source it spends most of its time in
// syscalls (roughly 5x slower decoding a multi-hundred-megabyte image).
const bufSize = 4 << 20

// Decompress streams xz-compressed data from in to out, aborting with the
// context error if ctx is cancelled mid-decode.
func Decompress(ctx context.Context, in io.Reader, out io.Writer) error {
	r, err := ulikunitz.NewReader(bufio.NewReaderSize(in, bufSize))
	if err != nil {
		return fmt.Errorf("initializing xz reader: %w", err)
	}
	if _, err := io.Copy(out, &ctxReader{ctx: ctx, r: r}); err != nil {
		return fmt.Errorf("decompressing xz stream: %w", err)
	}
	return nil
}

// ctxReader makes the otherwise-uninterruptible decode cancellable: each Read
// returns the context error once ctx is cancelled, which unwinds io.Copy. The
// decode is single-threaded and can run for tens of seconds on a large image,
// so this is what lets a service shutdown propagate instead of blocking on it.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// DecompressFile decompresses the xz file at src to dst through
// DecompressReader.
func DecompressFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	return DecompressReader(ctx, in, dst)
}

// DecompressReader decompresses the xz stream from in to dst. It decodes into
// a temporary file in dst's directory and renames it into place, so an
// interrupted decode never leaves a partial dst that downstream code would
// mistake for a complete image.
func DecompressReader(ctx context.Context, in io.Reader, dst string) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err = Decompress(ctx, in, tmp); err != nil {
		return err
	}
	// os.CreateTemp creates the file 0o600. Match Lima's 0o644 for decompressed
	// images so the result stays readable beyond its owner.
	if err = tmp.Chmod(0o644); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}
