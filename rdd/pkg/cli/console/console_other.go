// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

// Package console repairs the controlling console after a child process leaves
// it in a broken output mode.
package console

import "io"

// Repair is a no-op: non-Windows consoles have no newline-auto-return bit.
func Repair() {}

// RepairingWriter returns w unchanged on non-Windows.
func RepairingWriter(w io.Writer) io.Writer { return w }
