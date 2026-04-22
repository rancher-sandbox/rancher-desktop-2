// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail

package watch

// FileChanges groups the channels a FileWatcher uses to signal file
// modifications, truncations, and deletions. Each channel has a
// buffer of 1: a notification arriving while a prior one is still
// pending is dropped (coalesced). Consumers that must observe every
// event cannot rely on FileChanges alone.
type FileChanges struct {
	Modified  chan bool // Channel to get notified of modifications
	Truncated chan bool // Channel to get notified of truncations
	Deleted   chan bool // Channel to get notified of deletions/renames
}

// NewFileChanges constructs a FileChanges with all three notification
// channels initialized to a buffered size of 1.
func NewFileChanges() *FileChanges {
	return &FileChanges{
		make(chan bool, 1), make(chan bool, 1), make(chan bool, 1),
	}
}

// NotifyModified signals an append/modification if no prior notification
// is still pending on the Modified channel.
func (fc *FileChanges) NotifyModified() {
	sendOnlyIfEmpty(fc.Modified)
}

// NotifyTruncated signals a truncation if no prior notification is still
// pending on the Truncated channel.
func (fc *FileChanges) NotifyTruncated() {
	sendOnlyIfEmpty(fc.Truncated)
}

// NotifyDeleted signals a deletion/rename if no prior notification is
// still pending on the Deleted channel.
func (fc *FileChanges) NotifyDeleted() {
	sendOnlyIfEmpty(fc.Deleted)
}

// sendOnlyIfEmpty sends on a bool channel only if the channel has no
// backlog to be read by other goroutines. This concurrency pattern
// can be used to notify other goroutines if and only if they are
// looking for it (i.e., subsequent notifications can be compressed
// into one).
func sendOnlyIfEmpty(ch chan bool) {
	select {
	case ch <- true:
	default:
	}
}
