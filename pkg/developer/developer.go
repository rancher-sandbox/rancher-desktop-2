// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package developer detects whether the process runs in developer mode.
// Developer mode is enabled by the RDD_DEVELOPER_MODE environment variable or
// by detecting the source tree relative to the executable, and exposes hidden
// CLI flags for development.
package developer

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Mode returns true if RDD is running in developer mode.
// Developer mode is enabled if:
// 1. RDD_DEVELOPER_MODE environment variable is set to a true value, OR
// 2. RDD_DEVELOPER_MODE is not set AND ../cmd/rdd/main.go exists relative to the current executable.
var Mode = sync.OnceValue(func() bool {
	if envVar := os.Getenv("RDD_DEVELOPER_MODE"); envVar != "" {
		if b, err := strconv.ParseBool(strings.TrimSpace(envVar)); err == nil {
			return b
		}
		// If parsing fails, treat as false
		return false
	}

	execPath, err := os.Executable()
	if err != nil {
		return false
	}
	execDir := filepath.Dir(execPath)
	mainGoPath := filepath.Join(execDir, "..", "cmd", "rdd", "main.go")
	_, err = os.Stat(mainGoPath)
	return err == nil
})
