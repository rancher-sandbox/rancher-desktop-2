// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// spawn implements [commandImpl.spawn] for macOS.
func (c *concreteCommandExecutor) spawn(ctx context.Context, opts spawnOptions) error {
	// On darwin, we set the controlling TTY to a PTY, and stash the master end.
	// When the child exits, we close the master end.  If we exit before the child,
	// then the OS closes the master end for us, which sends SIGHUP to the child.

	var success bool
	var temporaryWorkingDir string
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("failed to open PTY master: %w", err)
	}
	defer func() {
		if success {
			c.cleanup = sync.OnceValue(func() error {
				var errs []error
				errs = append(errs, master.Close(), c.forceKill(ctx, c.Process))
				if temporaryWorkingDir != "" {
					errs = append(errs, os.RemoveAll(temporaryWorkingDir))
				}
				return errors.Join(errs...)
			})
		} else {
			_ = master.Close()
			if temporaryWorkingDir != "" {
				_ = os.RemoveAll(temporaryWorkingDir)
			}
		}
	}()

	var slave *os.File
	makeCommand := func() (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, opts.executable, opts.args...)
		cmd.Dir = opts.dir
		cmd.Env = opts.env
		cmd.Stderr = opts.stderr

		if err := unix.IoctlSetInt(int(master.Fd()), unix.TIOCPTYGRANT, 0); err != nil {
			return nil, fmt.Errorf("ioctl TIOCPTYGRANT failed: %w", err)
		}
		if err := unix.IoctlSetInt(int(master.Fd()), unix.TIOCPTYUNLK, 0); err != nil {
			return nil, fmt.Errorf("ioctl TIOCPTYUNLK failed: %w", err)
		}
		var nameBuf [128]byte
		_, _, errno := unix.Syscall(
			//nolint:staticcheck // unix.SYS_IOCTL is deprecated without replacement.
			unix.SYS_IOCTL,
			master.Fd(),
			unix.TIOCPTYGNAME,
			uintptr(unsafe.Pointer(&nameBuf[0])),
		)
		if errno != 0 {
			return nil, fmt.Errorf("ioctl TIOCPTYGNAME failed: %w", errno)
		}

		slavePath, _, found := strings.Cut(string(nameBuf[:]), "\x00")
		if !found {
			return nil, fmt.Errorf("ioctl TIOCPTYGNAME not null terminated: %q", nameBuf)
		}
		slave, err = os.OpenFile(slavePath, os.O_RDWR|unix.O_NOCTTY, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to open PTY slave %q: %w", slavePath, err)
		}

		// Establish the slave as the controlling terminal for the child.
		ttyFD := len(cmd.ExtraFiles) + 3 // child fd is 3+i per .ExtraFiles docs.
		cmd.ExtraFiles = append(cmd.ExtraFiles, slave)
		cmd.SysProcAttr = &unix.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    ttyFD,
		}
		return cmd, nil
	}

	var cmd *exec.Cmd
	needTemporaryDir := opts.dir == ""
	if !needTemporaryDir {
		cmd, err = makeCommand()
		if err != nil {
			return err
		}
		err = cmd.Start()
		if errors.Is(err, os.ErrNotExist) {
			needTemporaryDir = true
			_ = slave.Close()
		}
	}
	if needTemporaryDir {
		cmd, err = makeCommand()
		if err != nil {
			return err
		}
		// The working directory is invalid
		temporaryWorkingDir, err = os.MkdirTemp("", "rdd-compose-*")
		if err != nil {
			_ = slave.Close()
			return fmt.Errorf("failed to create temporary working directory: %w", err)
		}
		cmd.Dir = temporaryWorkingDir
		err = cmd.Start()
	}

	// We must close our handle to the slave end in RDD as soon as the
	// child starts, so that when the child exits, the last write handle
	// to the PTY is closed and the master end gets EOF.
	_ = slave.Close()
	if err != nil {
		return err
	}

	go func() {
		_, _ = io.Copy(io.Discard, master)
	}()

	c.Cmd = cmd
	success = true
	return nil
}

// kill implements [command].
func (c *concreteCommandExecutor) kill(ctx context.Context) error {
	process := c.Process
	if process == nil {
		return nil
	}
	// On macOS, since we're using PTYs, we can just close the master end; this
	// causes the child to receive a SIGHUP.
	if cleanup := c.cleanup; cleanup != nil {
		return cleanup()
	}
	// No cleanup; should not happen.
	err := process.Signal(unix.SIGTERM)
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return c.forceKill(ctx, process)
}

func (c *concreteCommandExecutor) forceKill(ctx context.Context, process *os.Process) error {
	killCtx, cancel := context.WithTimeout(ctx, killTimeout)
	defer cancel()
	select {
	case <-c.done:
	case <-killCtx.Done():
		// In case the child is ignoring SIGHUP
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
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
