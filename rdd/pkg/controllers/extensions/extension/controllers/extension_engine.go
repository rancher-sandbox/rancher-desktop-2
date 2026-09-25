// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

type result[T any] struct {
	value T
	err   error
}

func success[T any](value T) result[T] {
	return result[T]{value: value, err: nil}
}

func failure[T any](err error) result[T] {
	return result[T]{err: err}
}

// engineExportProgress is used to report status from the [engine.export] operation.
type engineExportProgress int

// The possible values for engineExportProgress reported via the result channel.
const (
	engineExportProgressStarted = engineExportProgress(iota)
	engineExportProgressContainerCreated
	engineExportProgressCopying
	engineExportProgressCompleted
)

type engineExportOptions struct {
	// The ID of the container, returned by createForExport.
	id string
	// The directory to place the exported files.  It must exist and be writable.
	destDir string
	// The relative path within the image to export.
	sourcePath string
	// Whether the source path is a directory; if the actual result does not match,
	// it is an error and nothing is extracted.
	isDirectory bool
	// The channel to report export progress.  The caller must ensure this channel
	// does not block.  Once the export call returns nil (before the export
	// finishes), the callee is responsible for closing the channel.
	ch chan<- result[engineExportProgress]
}

// engineCreateForExportResult represents the result of [engine.createForExport].
type engineCreateForExportResult struct {
	// Container ID; to be passed via [engineExportOptions.id].
	id string
	// Cleanup function; must be called after all [engine.export] calls have been
	// completed.
	cleanup func()
}

type engine interface {
	// Connect to the engine; this should be triggered on App changes.
	connect(context.Context) error
	// Create a container for the given extension, without starting.  This is
	// meant for use with export; if exporting does not require a container, this
	// method may be a no-op.  The given context must be valid past the point the
	// container is needed so that it can be cleaned up.
	createForExport(ctx context.Context, ext *v1alpha1.Extension) (engineCreateForExportResult, error)
	// Export the container image for the given extension to the specified
	// directory.  Only regular files and directories are copied; ownership is
	// ignored.  The passed-in context must be valid for the duration of the
	// export operation, which extends beyond returning to the caller.
	export(ctx context.Context, opts engineExportOptions) error
}
