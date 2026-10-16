// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package ringbuf implements a simple ring buffer implementing [io.Writer],
// such that only the last N bytes written are retained.
package ringbuf

import (
	"fmt"
	"slices"
)

// RingBuffer is a simple ring buffer implementing [io.Writer], such that only
// the last N bytes written are retained.
// This is not concurrency safe.
type RingBuffer struct {
	buf    []byte
	size   int
	start  int
	length int
}

// New creates a new RingBuffer with the given size, which must be positive.
func New(size int) *RingBuffer {
	if size < 1 {
		panic(fmt.Sprintf("invalid ring buffer size: %d", size))
	}
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write implements [io.Writer].  It writes the given bytes to the ring buffer,
// overwriting the oldest bytes if necessary.  It returns the number of bytes
// written, which is always len(p).
func (r *RingBuffer) Write(p []byte) (n int, err error) {
	n = len(p)
	if n >= r.size {
		copy(r.buf, p[n-r.size:])
		r.start = 0
		r.length = r.size
		return n, nil
	}

	end := (r.start + r.length) % r.size
	if end+n <= r.size {
		copy(r.buf[end:end+n], p)
	} else {
		chunkSize := r.size - end
		copy(r.buf[end:], p[:chunkSize])
		copy(r.buf[0:n-chunkSize], p[chunkSize:])
	}
	r.length += n
	if r.length > r.size {
		r.start = (r.start + r.length - r.size) % r.size
		r.length = r.size
	}
	return n, nil
}

// Bytes returns a slice containing the current contents of the ring buffer.
func (r *RingBuffer) Bytes() []byte {
	if r.length == 0 {
		return nil
	}
	if r.start+r.length <= r.size {
		// Return a copy, to avoid the caller mutating our buffer.
		return slices.Clone(r.buf[r.start : r.start+r.length])
	}
	return slices.Concat(r.buf[r.start:], r.buf[:r.length-(r.size-r.start)])
}
