// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
//go:build windows

package nxadmtail

import (
	"os"

	"github.com/nxadm/tail/winfile"
)

// openFile proxies an os.Open call for a file so it can be correctly tailed
// on POSIX and non-POSIX OSes like MS Windows.
func openFile(name string) (file *os.File, err error) {
	return winfile.OpenFile(name, os.O_RDONLY, 0)
}
