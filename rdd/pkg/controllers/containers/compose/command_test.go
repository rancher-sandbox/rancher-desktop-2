// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

const helperProcessEnv = "RDD_COMPOSEPROJECT_HELPER_PROCESS"

func TestDefaultCommandExecutor(t *testing.T) {
	t.Setenv(helperProcessEnv, "success")

	var commandExecutor commandExecutor = defaultCommandExecutor

	args := []string{
		os.Args[0],
		"-test.run=^TestCommandExecutorHelper$",
	}
	cmd, err := commandExecutor(t.Context(), t.TempDir(), args[0], args[1:]...)
	assert.NilError(t, err)
	assert.DeepEqual(t, args, cmd.args())
	assert.NilError(t, cmd.wait(t.Context()))
	assert.Equal(t, "", cmd.output(), "unexpected output")
}

func TestDefaultCommandExecutorCapturesStderr(t *testing.T) {
	t.Setenv(helperProcessEnv, "error")

	cmd, err := defaultCommandExecutor(
		t.Context(),
		t.TempDir(),
		os.Args[0],
		"-test.run=^TestCommandExecutorHelper$",
	)
	assert.NilError(t, err)
	err = cmd.wait(t.Context())
	var exitErr *exec.ExitError
	assert.Assert(t, errors.As(err, &exitErr), "expected an ExitError, got %T", err)
	assert.Equal(t, strings.TrimSpace(cmd.output()), helperProcessEnv, "unexpected output")
}

func TestDefaultCommandExecutorKill(t *testing.T) {
	t.Setenv(helperProcessEnv, "sleep")

	cmd, err := defaultCommandExecutor(
		t.Context(),
		t.TempDir(),
		os.Args[0],
		"-test.run=^TestCommandExecutorHelper$",
	)
	assert.NilError(t, err)
	// Set the time out a bit long, because it may need to compile the test.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	assert.NilError(t, cmd.kill(ctx))
	// The context should not have been canceled.
	assert.NilError(t, ctx.Err())
	// Wait should not take very long here.  However, it may return an error when
	// the process terminates, because it was done forcefully.
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_ = cmd.wait(ctx)
	// The context should not have been canceled.
	assert.NilError(t, ctx.Err())
}

// TestCommandExecutorHelper is a helper process that is used to test the
// commandExecutor implementation.  It is not a real test, and should not be run
// directly.
func TestCommandExecutorHelper(t *testing.T) {
	switch os.Getenv(helperProcessEnv) {
	case "success":
	case "error":
		_, err := os.Stderr.WriteString(helperProcessEnv)
		assert.NilError(t, err)
		assert.Assert(t, false, "Forcing exit")
	case "sleep":
		time.Sleep(time.Hour)
	}
	// Ignore the default case: this is being run as a test.
}
