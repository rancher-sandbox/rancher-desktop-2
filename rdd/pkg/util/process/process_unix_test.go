//go:build unix

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

// TestIsAlive confirms liveness detection on Unix: the running test process is
// alive, and an unassigned PID is not.
func TestIsAlive(t *testing.T) {
	assert.Assert(t, IsAlive(os.Getpid()), "the test process is alive")
	assert.Assert(t, !IsAlive(0x7FFFFFF0), "an unassigned pid is not alive")
}

// TestIsOurProcessNoOp confirms the Unix identity check is a no-op that returns
// true regardless of key or PID; on Unix, Interrupt uses signals, so there is no
// per-process registration to consult.
func TestIsOurProcessNoOp(t *testing.T) {
	assert.Assert(t, IsOurProcess(ServeInterruptKey, os.Getpid()))
	assert.Assert(t, IsOurProcess(HostagentInterruptKey("rd"), 0x7FFFFFF0))
}
