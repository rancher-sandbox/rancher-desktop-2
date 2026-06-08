//go:build unix

// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import "context"

// ensureSSHKeys is a no-op on Unix. Lima's DefaultPubKeys generates the
// SSH keypair correctly on Unix; the workaround is only needed on Windows
// where Lima's cygpath-based path conversion breaks with Windows OpenSSH.
func ensureSSHKeys(_ context.Context) error {
	return nil
}
