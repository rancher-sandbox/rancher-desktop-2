// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package ringbuf_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/ringbuf"
)

func TestRingBuffer(t *testing.T) {
	r := ringbuf.New(5)

	// Test full write
	n, err := r.Write([]byte("hello"))
	assert.NilError(t, err)
	assert.Equal(t, n, 5)
	assert.Equal(t, string(r.Bytes()), "hello")

	// Test overlength write
	n, err = r.Write([]byte("world!"))
	assert.NilError(t, err)
	assert.Equal(t, n, 6)
	assert.Equal(t, string(r.Bytes()), "orld!")

	// Test underlength write
	n, err = r.Write([]byte("abc"))
	assert.NilError(t, err)
	assert.Equal(t, n, 3)
	assert.Equal(t, string(r.Bytes()), "d!abc")

	// Test wraparound write
	n, err = r.Write([]byte("defg"))
	assert.NilError(t, err)
	assert.Equal(t, n, 4)
	assert.Equal(t, string(r.Bytes()), "cdefg")
}
