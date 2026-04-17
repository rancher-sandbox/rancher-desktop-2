// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

// Package watch provides the FileWatcher abstraction used by the
// vendored nxadm/tail to observe changes to a file.
package watch

import "gopkg.in/tomb.v1"

// FileWatcher monitors file-level events.
type FileWatcher interface {
	// BlockUntilExists blocks until the file comes into existence.
	BlockUntilExists(*tomb.Tomb) error

	// ChangeEvents reports on changes to a file, be it modification,
	// deletion, renames or truncations. The returned FileChanges
	// channels will be closed, thus become unusable, after a deletion
	// or truncation event.
	// In order to properly report truncations, ChangeEvents requires
	// the caller to pass their current offset in the file.
	ChangeEvents(*tomb.Tomb, int64) (*FileChanges, error)
}
