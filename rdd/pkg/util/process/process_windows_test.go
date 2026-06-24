// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"os"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// TestRegisterInterruptHandlerAndInterrupt confirms the named-event round trip:
// after a process registers a handler for a key, Interrupt(key, pid) fires it
// and IsOurProcess(key, pid) recognises it. This is the mechanism that delivers
// a graceful shutdown across consoles.
func TestRegisterInterruptHandlerAndInterrupt(t *testing.T) {
	const key = ServeInterruptKey
	fired := make(chan struct{})
	release, err := RegisterInterruptHandler(key, func() { close(fired) })
	assert.NilError(t, err)
	defer release()

	assert.Assert(t, IsOurProcess(key, os.Getpid()),
		"a process that registered key %q should be recognised as ours", key)
	assert.Assert(t, !IsOurProcess(HostagentInterruptKey("rd"), os.Getpid()),
		"a key the process did not register must not match")

	assert.NilError(t, Interrupt(key, os.Getpid()))

	var ok bool
	select {
	case <-fired:
		ok = true
	case <-time.After(10 * time.Second):
	}
	assert.Assert(t, ok, "interrupt handler did not fire within 10s")
}

// TestInterruptUnregisteredFails confirms Interrupt and IsOurProcess reject a PID
// with no registered event — a dead, recycled, or unrelated process — so callers
// fall back to a force kill instead of disturbing it.
func TestInterruptUnregisteredFails(t *testing.T) {
	assert.Assert(t, Interrupt(ServeInterruptKey, 0x7FFFFFF0) != nil,
		"interrupting a pid with no registered event should fail")
	assert.Assert(t, !IsOurProcess(ServeInterruptKey, 0x7FFFFFF0),
		"a pid with no registered event is not ours")
}

// TestIsAlive confirms liveness detection on Windows: the running test process
// is alive, and an unassigned PID is not.
func TestIsAlive(t *testing.T) {
	assert.Assert(t, IsAlive(os.Getpid()), "the test process is alive")
	assert.Assert(t, !IsAlive(0x7FFFFFF0), "an unassigned pid is not alive")
}
