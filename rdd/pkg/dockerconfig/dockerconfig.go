// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package dockerconfig provides serialized access to the Docker CLI's
// config.json, which is shared across Rancher Desktop instances and, within a
// daemon, by the engine controller (currentContext) and the path-management
// controller (cliPluginsExtraDirs). Edit takes a cross-process lock so a
// concurrent read-modify-write cannot drop another writer's edit.
package dockerconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/gofrs/flock"
)

const lockFile = "rancher-desktop-dockerconfig.lock"

const lockTimeout = 30 * time.Second

// ErrMalformed marks a config.json that docker/cli cannot parse. Only the user
// can repair it, so retrying won't help.
var ErrMalformed = errors.New("docker config.json is malformed")

// Dir returns $DOCKER_CONFIG when set, otherwise ~/.docker. It avoids
// config.Dir(), whose sync.Once cache defeats t.Setenv("HOME", …) in tests.
func Dir() (string, error) {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".docker"), nil
}

// sharedDataDir returns the per-user directory holding Rancher Desktop's
// cross-process locks, matching the path-management controller's location.
func sharedDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local"), nil
	case "linux":
		return filepath.Join(home, ".local", "share"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	default:
		return "", fmt.Errorf("platform %s not supported", runtime.GOOS)
	}
}

func lock(ctx context.Context) (func(), error) {
	dir, err := sharedDataDir()
	if err != nil {
		return nil, fmt.Errorf("locate docker config lock directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create docker config lock directory: %w", err)
	}
	fl := flock.New(filepath.Join(dir, lockFile))

	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	locked, err := fl.TryLockContext(lockCtx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire docker config lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("acquire docker config lock: timed out after %s", lockTimeout)
	}
	return func() { _ = fl.Unlock() }, nil
}

// Edit locks config.json, loads it, applies fn, and saves only when fn reports a
// change. config.Load validates every "auths" entry, so a malformed credential
// fails Edit until the user repairs config.json. Parse failures wrap ErrMalformed.
func Edit(ctx context.Context, fn func(cf *configfile.ConfigFile) (changed bool, err error)) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	unlock, err := lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	cf, err := config.Load(dir)
	if err != nil {
		// config.Load wraps an *fs.PathError around open and read failures; any
		// other error comes from parsing the file.
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) {
			// Wrap ErrMalformed alone: a caller walking Unwrap() []error would
			// read a second %w operand as an independent cause.
			return fmt.Errorf("%w: %s", ErrMalformed, err.Error())
		}
		return err
	}
	changed, err := fn(cf)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return cf.Save()
}
