// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestEnsureSSHKeysAt(t *testing.T) {
	ctx := context.Background()

	t.Run("both keys exist", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")
		// Generate a real keypair first.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		privBefore, err := os.ReadFile(privPath)
		assert.NilError(t, err)

		// Calling again should be a no-op.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		privAfter, err := os.ReadFile(privPath)
		assert.NilError(t, err)
		assert.DeepEqual(t, privBefore, privAfter)
	})

	t.Run("missing public key derives from private key", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")
		pubPath := privPath + ".pub"

		// Generate a real keypair, then remove the public key.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		privBefore, err := os.ReadFile(privPath)
		assert.NilError(t, err)
		assert.NilError(t, os.Remove(pubPath))

		// Should derive the public key without regenerating the private key.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		privAfter, err := os.ReadFile(privPath)
		assert.NilError(t, err)
		assert.DeepEqual(t, privBefore, privAfter)
		_, err = os.Stat(pubPath)
		assert.NilError(t, err)
	})

	t.Run("corrupt private key triggers full regeneration", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")
		pubPath := privPath + ".pub"

		// Write a corrupt private key (no public key).
		assert.NilError(t, os.WriteFile(privPath, []byte("not a key"), 0o600))

		// Should remove the corrupt key and generate a fresh pair.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		priv, err := os.ReadFile(privPath)
		assert.NilError(t, err)
		assert.Assert(t, len(priv) > 20, "private key should be a real key, got %d bytes", len(priv))
		_, err = os.Stat(pubPath)
		assert.NilError(t, err)
	})

	t.Run("stale temp files from prior crash", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")
		tmpPath := privPath + ".tmp"

		// Simulate a crash that left temp files behind.
		assert.NilError(t, os.WriteFile(tmpPath, []byte("stale"), 0o600))
		assert.NilError(t, os.WriteFile(tmpPath+".pub", []byte("stale"), 0o600))

		// Should clean up stale temps and generate fresh keys.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		_, err := os.Stat(privPath)
		assert.NilError(t, err)
		_, err = os.Stat(privPath + ".pub")
		assert.NilError(t, err)
		// Temp files should be gone.
		_, err = os.Stat(tmpPath)
		assert.Assert(t, os.IsNotExist(err))
		_, err = os.Stat(tmpPath + ".pub")
		assert.Assert(t, os.IsNotExist(err))
	})

	t.Run("orphaned public key without private key", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")
		pubPath := privPath + ".pub"

		// Only the public key exists (e.g. from a partial rename failure).
		assert.NilError(t, os.WriteFile(pubPath, []byte("orphan"), 0o644))

		// Should remove the orphaned pubkey and generate a fresh pair.
		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		_, err := os.Stat(privPath)
		assert.NilError(t, err)
		_, err = os.Stat(pubPath)
		assert.NilError(t, err)
	})

	t.Run("no keys exist", func(t *testing.T) {
		dir := t.TempDir()
		privPath := filepath.Join(dir, "user")

		assert.NilError(t, ensureSSHKeysAt(ctx, privPath))
		_, err := os.Stat(privPath)
		assert.NilError(t, err)
		_, err = os.Stat(privPath + ".pub")
		assert.NilError(t, err)
	})
}
