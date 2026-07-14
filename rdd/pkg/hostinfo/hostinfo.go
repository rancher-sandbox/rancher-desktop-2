// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package hostinfo detects the host hardware limits (CPU count and total
// memory) that back both the HostInfo CRD Status advertised to the GUI and the
// ceilings enforced by the App admission webhooks. Keeping a single Detect()
// ensures those two contracts can never drift apart.
package hostinfo

import (
	goruntime "runtime"

	"github.com/pbnjay/memory"
)

// HostInfo holds the detected host hardware limits.
type HostInfo struct {
	// CPUs is the number of logical CPUs on the host.
	CPUs int
	// Memory is the total host memory in bytes.
	Memory int64
}

// Detect reads the host CPU count and total memory.
func Detect() HostInfo {
	return HostInfo{
		CPUs:   goruntime.NumCPU(),
		Memory: int64(memory.TotalMemory()),
	}
}
