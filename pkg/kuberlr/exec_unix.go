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

// Exec replaces the current process with kubectl at path, passing args and
// the current environment plus a recursion guard. The kubectl process
// inherits our PID, so the shell sees its exit status directly. ctx is
// accepted for signature parity with the Windows variant; syscall.Exec
// replaces the process so cancellation no longer applies.
func Exec(_ context.Context, path string, args []string) error {
	env := append(os.Environ(), envSkipResolver+"=1")
	if err := syscall.Exec(path, append([]string{path}, args...), env); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil
}
