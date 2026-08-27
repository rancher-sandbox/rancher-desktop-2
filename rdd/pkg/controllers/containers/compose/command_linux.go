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
	"runtime"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// spawn implements [commandImpl.spawn] for Linux.
func (c *concreteCommandExecutor) spawn(ctx context.Context, opts spawnOptions) error {
	// On Linux, we use Pdeathsig to send a signal to the child when we exit.
	// However, that means we need to lock the thread; that will block until the
	// process exits.

	spawnCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		cmd := exec.CommandContext(ctx, opts.executable, opts.args...)
		cmd.Dir = opts.dir
		cmd.Env = opts.env
		cmd.Stderr = opts.stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGTERM,
			Setpgid:   true,
		}
		var err error
		needTemporaryDir := opts.dir == ""
		if !needTemporaryDir {
			err = cmd.Start()
			if errors.Is(err, os.ErrNotExist) {
				// Either the directory does not exist, or the executable does not; try
				// again with a temporary working directory.
				needTemporaryDir = true
			}
		}
		if needTemporaryDir {
			// We may have started the command already; recreate it.
			cmd = exec.CommandContext(ctx, opts.executable, opts.args...)
			cmd.Env = opts.env
			cmd.Stderr = opts.stderr
			cmd.SysProcAttr = &syscall.SysProcAttr{
				Pdeathsig: syscall.SIGTERM,
				Setpgid:   true,
			}
			var temporaryWorkingDir string
			temporaryWorkingDir, err = os.MkdirTemp("", "rdd-compose-*")
			if err != nil {
				spawnCh <- fmt.Errorf("failed to create temporary working directory: %w", err)
				close(spawnCh)
				return
			}
			defer func() {
				_ = os.RemoveAll(temporaryWorkingDir)
			}()
			cmd.Dir = temporaryWorkingDir
			err = cmd.Start()
		}
		c.Cmd = cmd

		waitCh := make(chan struct{}, 1)
		c.cleanup = sync.OnceValue(func() error {
			close(waitCh)
			return nil
		})

		spawnCh <- err
		close(spawnCh)
		if err != nil {
			return
		}

		<-waitCh
	}()

	return <-spawnCh
}

// kill implements [command].
func (c *concreteCommandExecutor) kill(ctx context.Context) error {
	process := c.Process
	if process == nil {
		return nil
	}
	killCtx, cancel := context.WithTimeout(ctx, killTimeout)
	defer cancel()
	var innerErr error
	err := process.WithHandle(func(handle uintptr) {
		// We use PIDFD_SIGNAL_PROCESS_GROUP to send the signal to the entire
		// process group.  This is supported as of Linux 6.9; that covers all
		// distributions we want to cover.
		innerErr = unix.PidfdSendSignal(int(handle), syscall.SIGTERM, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP)
		if innerErr != nil {
			return
		}
		select {
		case <-c.done:
		case <-killCtx.Done():
			innerErr = unix.PidfdSendSignal(int(handle), syscall.SIGKILL, nil, unix.PIDFD_SIGNAL_PROCESS_GROUP)
		}
	})
	// err may be [os.ErrNoHandle] on obsolete versions of Linux we don't care
	// about; just return the error if that happens.
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		err = innerErr
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, killTimeout)
	defer waitCancel()
	select {
	case <-c.done:
	case <-waitCtx.Done():
		return fmt.Errorf("timed out waiting for process to be reaped: %w", waitCtx.Err())
	}
	return nil
}
