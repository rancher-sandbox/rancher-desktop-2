// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

package atomicfile

import (
	"os"
	"syscall"
)

// copyOwner transfers src's ownership to dst, so a privileged process
// replacing another user's file keeps that user as the owner. Best effort;
// failures are ignored.
func copyOwner(src os.FileInfo, dst string) {
	if st, ok := src.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(dst, int(st.Uid), int(st.Gid))
	}
}
