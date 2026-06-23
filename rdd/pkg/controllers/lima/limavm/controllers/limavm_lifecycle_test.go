// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestDecideUnwatchedAction(t *testing.T) {
	cases := []struct {
		name             string
		hasLiveHostagent bool
		shouldRun        bool
		want             unwatchedAction
	}{
		{"live orphan, should run", true, true, actionKillOrphan},
		{"live orphan, should stop", true, false, actionKillOrphan},
		// No live hostagent: there is nothing to wait for, so we must not enter
		// the kill-and-requeue path. A Broken instance with no live hostagent
		// (e.g. WSL reporting "no installed distributions") lands here and used
		// to spin forever killing a process that does not exist.
		{"no hostagent, should run", false, true, actionStart},
		{"no hostagent, should stop", false, false, actionMarkStopped},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideUnwatchedAction(tc.hasLiveHostagent, tc.shouldRun)
			assert.Equal(t, got, tc.want)
		})
	}
}
