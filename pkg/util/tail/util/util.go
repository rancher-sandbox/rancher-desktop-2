// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: 2019 FOSS contributors of https://github.com/nxadm/tail
// SPDX-FileCopyrightText: 2015 HPE Software Inc.
// SPDX-FileCopyrightText: 2013 ActiveState Software Inc.

// Package util contains small helpers used internally by the vendored
// nxadm/tail code.
package util

import (
	"fmt"
	"log"
	"os"
	"runtime/debug"
)

// Logger wraps the standard log.Logger used by this package.
type Logger struct {
	*log.Logger
}

// LOGGER is the default logger used by Fatal.
var LOGGER = &Logger{log.New(os.Stderr, "", log.LstdFlags)}

// Fatal logs the formatted message with a stack trace for the current
// goroutine and terminates the process with exit status 1.
func Fatal(format string, v ...interface{}) {
	// https://github.com/nxadm/log/blob/master/log.go#L45
	LOGGER.Output(2, fmt.Sprintf("FATAL -- "+format, v...)+"\n"+string(debug.Stack()))
	os.Exit(1)
}

// PartitionString splits s into chunks of chunkSize bytes; the last
// chunk may be shorter. Panics if chunkSize <= 0.
func PartitionString(s string, chunkSize int) []string {
	if chunkSize <= 0 {
		panic("invalid chunkSize")
	}
	length := len(s)
	chunks := 1 + length/chunkSize
	start := 0
	end := chunkSize
	parts := make([]string, 0, chunks)
	for {
		if end > length {
			end = length
		}
		parts = append(parts, s[start:end])
		if end == length {
			break
		}
		start, end = end, end+chunkSize
	}
	return parts
}
