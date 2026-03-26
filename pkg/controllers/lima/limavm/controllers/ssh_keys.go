// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ensureSSHKeysAt generates an SSH keypair at privPath (and privPath+".pub")
// if it doesn't already exist. Handles partial on-disk state from prior
// crashes: missing public key, stale temp files, and corrupt private keys.
//
// Unlike Lima's DefaultPubKeys, this does not take an inter-process lock
// because only one lima-controller runs per RDD instance.
func ensureSSHKeysAt(ctx context.Context, privPath string) error {
	// Bound ssh-keygen calls so a misconfigured system (e.g. hardware
	// security module prompts, broken MSYS2 path conversion) cannot
	// block controller setup indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pubPath := privPath + ".pub"
	if _, err := os.Stat(privPath); err == nil {
		if _, err := os.Stat(pubPath); err == nil {
			return nil
		}
		// Public key missing — try to derive it from the existing private key
		// to preserve SSH access to existing VMs. Fall back to full regeneration
		// if the private key is corrupt.
		if pub, err := exec.CommandContext(ctx, "ssh-keygen", "-y", "-f", privPath).Output(); err == nil {
			return os.WriteFile(pubPath, pub, 0o644)
		}
		_ = os.Remove(privPath)
	}
	// Remove orphaned public key (e.g. from a previous partial rename
	// failure). On Windows, os.Rename fails if the destination exists.
	_ = os.Remove(pubPath)
	configDir := filepath.Dir(privPath)
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("could not create %q: %w", configDir, err)
	}
	// Generate into a temporary path and rename on success, so an
	// interrupted ssh-keygen does not leave a partial key that blocks
	// future attempts.
	tmpPath := privPath + ".tmp"
	// Clean up stale temp files from a prior crash so ssh-keygen does not
	// prompt "Overwrite (y/n)?" and hang waiting for a TTY that doesn't exist.
	_ = os.Remove(tmpPath)
	_ = os.Remove(tmpPath + ".pub")
	cmd := exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-q", "-N", "",
		"-C", "lima", "-f", tmpPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Clean up any partial files left by ssh-keygen.
		_ = os.Remove(tmpPath)
		_ = os.Remove(tmpPath + ".pub")
		return fmt.Errorf("failed to generate SSH key: %s: %w", out, err)
	}
	if err := os.Rename(tmpPath+".pub", pubPath); err != nil {
		_ = os.Remove(tmpPath)
		_ = os.Remove(tmpPath + ".pub")
		return fmt.Errorf("failed to rename public key: %w", err)
	}
	if err := os.Rename(tmpPath, privPath); err != nil {
		_ = os.Remove(pubPath)
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to rename private key: %w", err)
	}
	return nil
}
