// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"path/filepath"

	"github.com/lima-vm/lima/v2/pkg/limatype/dirnames"
	"github.com/lima-vm/lima/v2/pkg/limatype/filenames"
)

// ensureSSHKeys generates Lima's SSH keypair if it doesn't exist.
// Lima's DefaultPubKeys uses cygpath to convert the key path to MSYS2 format
// (/c/...), which only works with MSYS2's ssh-keygen. Windows OpenSSH's
// ssh-keygen doesn't understand MSYS2 paths and fails with "No such file".
// By pre-generating the key with a native Windows path, Lima finds the
// existing key and skips its broken keygen path.
func ensureSSHKeys(ctx context.Context) error {
	configDir, err := dirnames.LimaConfigDir()
	if err != nil {
		return err
	}
	return ensureSSHKeysAt(ctx, filepath.Join(configDir, filenames.UserPrivateKey))
}
