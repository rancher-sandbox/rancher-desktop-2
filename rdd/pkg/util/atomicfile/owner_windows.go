// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build windows

package atomicfile

import "os"

// copyOwner is a no-op: Windows file ownership does not transfer via chown.
func copyOwner(_ os.FileInfo, _ string) {}
