// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestagent

import (
	_ "embed"
	"fmt"
	"os"
)

//go:embed lima-guestagent.gz
var guestAgentGZ []byte

// WriteTempFile writes the embedded guest agent to a temporary file
// and returns the path and a cleanup function. The caller must call
// cleanup when the file is no longer needed.
//
// The file has a .gz suffix so Lima's hostagent decompresses it
// before copying it into the guest.
func WriteTempFile() (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "lima-guestagent-*.gz")
	if err != nil {
		return "", nil, fmt.Errorf("creating guest agent temp file: %w", err)
	}
	if _, err := f.Write(guestAgentGZ); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("writing guest agent temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("closing guest agent temp file: %w", err)
	}
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}
