// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package kuberlr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
)

// Exec runs kubectl at path as a child process and propagates its exit
// status. Windows lacks an equivalent of the unix exec() syscall, so rdd
// spawns kubectl, waits for it, and returns a *cliexit.Error so main can
// re-issue the kubectl exit code. Both processes share the console, so
// Ctrl+C and Ctrl+Break reach kubectl directly without explicit signal
// forwarding. The recursion guard prevents kubectl from looping through
// Resolve if it ever re-execs us.
func Exec(ctx context.Context, path string, args []string) error {
	// CommandContext hard-kills on ctx cancellation. The cobra context
	// is never canceled today; wiring signal-driven cancellation must
	// revisit this site, or rdd's hard-kill races kubectl's graceful
	// shutdown of the console-delivered Ctrl+C.
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), envSkipResolver+"=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &cliexit.Error{Code: exitErr.ExitCode()}
	}
	if err != nil {
		return fmt.Errorf("running %s: %w", path, err)
	}
	return nil
}
