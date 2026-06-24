// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package process

import (
	"testing"

	"gotest.tools/v3/assert"
)

// TestInterruptKeys pins the interrupt-key format. Both ends of an interrupt —
// the process that registers (the hostagent / daemon) and the one that signals
// or checks it (the lima controller / CLI) — must derive the same key, so the
// format is part of the contract.
func TestInterruptKeys(t *testing.T) {
	assert.Equal(t, ServeInterruptKey, "serve")
	assert.Equal(t, HostagentInterruptKey("rd"), "hostagent-rd")
	assert.Equal(t, HostagentInterruptKey("rancher-desktop-2"), "hostagent-rancher-desktop-2")
}
