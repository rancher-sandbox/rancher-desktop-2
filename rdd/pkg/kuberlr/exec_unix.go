// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

package kuberlr

import (
	"context"
	"fmt"
	"os"
	"syscall"
)

// Exec replaces the current process with kubectl plus a recursion
// guard, so the shell sees kubectl's exit status directly. ctx is
// unused (signature parity with the Windows variant only).
func Exec(_ context.Context, path string, args []string) error {
	env := append(os.Environ(), envSkipResolver+"=1")
	if err := syscall.Exec(path, append([]string{path}, args...), env); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil
}
