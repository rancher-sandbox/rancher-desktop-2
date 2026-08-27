// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// spawn implements [commandImpl.spawn] for Windows.
func (c *concreteCommandExecutor) spawn(ctx context.Context, opts spawnOptions) error {
	// On Windows, we create a job object and assign the child process to it.
	// This will automatically terminate the child when the parent exits.
	// However, we don't have access to the hooks needed to call
	// UpdateProcThreadAttribute before the child is created, so we have to assign
	// it to the job after it has already started.

	// This will need to be updated after golang 1.28 ships, or whichever release
	// is after https://github.com/golang/go/issues/80415 gets merged.  That
	// enables the process to be assigned to the job before starting.

	var success bool

	hJob, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create job object: %w", err)
	}
	defer func() {
		if success {
			c.cleanup = sync.OnceValue(func() error {
				return windows.CloseHandle(hJob)
			})
		} else {
			_ = windows.CloseHandle(hJob)
		}
	}()

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

	_, err = windows.SetInformationJobObject(
		hJob,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		return fmt.Errorf("failed to set job object information: %w", err)
	}

	makeCommand := func() (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, opts.executable, opts.args...)
		cmd.Dir = opts.dir
		cmd.Env = opts.env
		cmd.Stderr = opts.stderr
		return cmd, nil
	}

	var cmd *exec.Cmd
	var temporaryWorkingDir string
	needTemporaryDir := opts.dir == ""
	if !needTemporaryDir {
		cmd, err = makeCommand()
		if err != nil {
			return fmt.Errorf("failed to create command: %w", err)
		}
		err = cmd.Start()
		if errors.Is(err, windows.ERROR_DIRECTORY) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			needTemporaryDir = true
		}
	}
	if needTemporaryDir {
		temporaryWorkingDir, err = os.MkdirTemp("", "rdd-compose-*")
		if err != nil {
			return fmt.Errorf("failed to create temporary working directory: %w", err)
		}
		defer func() {
			if !success {
				_ = os.RemoveAll(temporaryWorkingDir)
			}
		}()
		cmd, err = makeCommand()
		if err != nil {
			return fmt.Errorf("failed to create command: %w", err)
		}
		cmd.Dir = temporaryWorkingDir
		err = cmd.Start()
	}
	if err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// WithHandle only guarantees that the handle is valid during the call;
	// make a copy that we can use.
	hProc := windows.InvalidHandle
	outerErr := cmd.Process.WithHandle(func(handle uintptr) {
		err = windows.DuplicateHandle(
			windows.CurrentProcess(),
			windows.Handle(handle),
			windows.CurrentProcess(),
			&hProc,
			0,
			false,
			windows.DUPLICATE_SAME_ACCESS)
	})
	if outerErr != nil || err != nil {
		// The process is not assigned to the job yet.
		return fmt.Errorf("failed to duplicate process handle: %w",
			errors.Join(outerErr, err, cmd.Process.Kill()))
	}

	err = windows.AssignProcessToJobObject(hJob, hProc)
	if err != nil {
		_ = windows.CloseHandle(hProc)
		return fmt.Errorf("failed to assign process to job object: %w",
			errors.Join(err, cmd.Process.Kill()))
	}

	go func() {
		_, _ = windows.WaitForSingleObject(hProc, windows.INFINITE)
		_ = windows.CloseHandle(hProc)
		_ = os.RemoveAll(temporaryWorkingDir)
	}()

	c.Cmd = cmd
	success = true

	return nil
}

// kill implements [command].
func (c *concreteCommandExecutor) kill(ctx context.Context) error {
	// Since we have a job, we can just close that to kill the child.
	if cleanup := c.cleanup; cleanup != nil {
		if err := cleanup(); err != nil {
			return err
		}
	} else {
		// Should not happen; but fall back to using the process directly.
		process := c.Process
		if process == nil {
			return nil
		}
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
	}
	killCtx, cancel := context.WithTimeout(ctx, killTimeout)
	defer cancel()
	select {
	case <-c.done:
	case <-killCtx.Done():
		// This shouldn't happen; it's here to ensure `killTimeout` gets used.
		return fmt.Errorf("timed out waiting for process to exit: %w", killCtx.Err())
	}
	return nil
}
