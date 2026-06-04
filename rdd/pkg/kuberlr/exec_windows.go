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

// Exec runs kubectl at path as a child process (Windows has no unix
// exec()) and returns a *cliexit.Error so main re-issues its exit code.
// Both processes share the console, so Ctrl+C/Break reach kubectl directly.
func Exec(ctx context.Context, path string, args []string) error {
	// CommandContext hard-kills on ctx cancellation; ctx is never canceled
	// today, but signal-driven cancellation later would race kubectl's
	// graceful Ctrl+C shutdown — revisit this site first.
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
