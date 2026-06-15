// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package console repairs the controlling console after a child process leaves
// it in a broken output mode.
package console

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// disableNewlineAutoReturn (DISABLE_NEWLINE_AUTO_RETURN) suppresses the implicit
// carriage return on a line feed. wsl.exe — Lima's WSL2 driver — sets it, with
// virtual-terminal processing, on the console it shares with us and never
// restores it, so every later '\n' moves down without returning to column 0:
// the "staircase".
const disableNewlineAutoReturn = 0x0008

// Repair clears DISABLE_NEWLINE_AUTO_RETURN on the controlling console, undoing
// the staircase a child leaves behind while preserving virtual-terminal
// processing (ANSI colors). Best-effort: a no-op when the console is redirected.
func Repair() {
	for _, f := range []*os.File{os.Stdout, os.Stderr} {
		h := windows.Handle(f.Fd())
		var mode uint32
		if windows.GetConsoleMode(h, &mode) != nil {
			continue // not a console handle (redirected)
		}
		if mode&disableNewlineAutoReturn != 0 {
			_ = windows.SetConsoleMode(h, mode&^disableNewlineAutoReturn)
		}
		return // stdout and stderr share one console buffer; one repair suffices
	}
}

// RepairingWriter wraps w so each Write first repairs the console mode, keeping
// logged lines from staircasing after a child corrupts the console.
func RepairingWriter(w io.Writer) io.Writer { return repairingWriter{w} }

type repairingWriter struct{ w io.Writer }

func (rw repairingWriter) Write(p []byte) (int, error) {
	Repair()
	return rw.w.Write(p)
}
