// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestexec

// This file rewrites Windows host paths to the /mnt/<drive> paths where
// the WSL2 guest automounts each drive. The functions parse plain strings
// instead of using path/filepath, whose rules follow the build platform,
// so they behave the same everywhere and their tests run on every
// platform. Callers apply them only on Windows. Only the hostCwd and
// hostDriveCwd hooks ask the host, and tests replace both.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// hostCwd returns the host working directory used to resolve relative
// paths; tests replace it.
var hostCwd = os.Getwd

// hostDriveCwd returns the current directory on drive, which a
// drive-relative path such as `d:foo` resolves against; tests replace it.
var hostDriveCwd = func(drive string) (string, error) {
	// On Windows, filepath.Abs gets it from GetFullPathName.
	return filepath.Abs(drive + ":")
}

// TranslateHostPath rewrites one host path to where the guest mounts it,
// e.g. `C:\Users\jan\app` to `/mnt/c/Users/jan/app`. A relative path
// resolves against the host working directory, and a drive-relative one
// (`d:foo`) against the current directory on its drive. UNC and POSIX-style
// absolute paths only get their backslashes turned into forward slashes.
func TranslateHostPath(arg string) (string, error) {
	slashed := strings.ReplaceAll(arg, `\`, "/")
	if _, _, ok := splitDrive(slashed); !ok && !strings.HasPrefix(slashed, "/") {
		// Join on the host side, so that cleaning the result stops `..` at
		// the drive root.
		base, rest, err := relativeBase(slashed)
		if err != nil {
			return "", err
		}
		slashed = strings.ReplaceAll(base, `\`, "/") + "/" + rest
	}
	if drive, rest, ok := splitDrive(slashed); ok {
		return "/mnt/" + drive + path.Clean("/"+rest), nil
	}
	// UNC (//server/share) or POSIX-style; nothing we can map.
	return slashed, nil
}

// relativeBase splits a relative host path into the host directory it
// resolves against and the part to join to that directory.
func relativeBase(slashed string) (base, rest string, err error) {
	if len(slashed) >= 2 && slashed[1] == ':' && isDriveLetter(slashed[0]) {
		base, err = hostDriveCwd(strings.ToLower(slashed[:1]))
		return base, slashed[2:], err
	}
	base, err = hostCwd()
	return base, slashed, err
}

// splitDrive splits `c:/Users/jan` into "c" and "/Users/jan". Only
// drive-absolute paths match; drive-relative ones (`c:foo`) do not.
func splitDrive(slashed string) (drive, rest string, ok bool) {
	if len(slashed) < 3 || slashed[1] != ':' || slashed[2] != '/' || !isDriveLetter(slashed[0]) {
		return "", "", false
	}
	return strings.ToLower(slashed[:1]), slashed[2:], true
}

// isDriveLetter reports whether c can name a Windows drive.
func isDriveLetter(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}
