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
// (`d:foo`) against the current directory on its drive. A rooted path
// without a drive (`\data`, `/data`) takes the root of the working
// directory, its drive or its `\\server\share`, as Windows resolves it. An
// extended-length path such as `\\?\C:\data` translates like `C:\data`. UNC
// paths only get their backslashes turned into forward slashes.
func TranslateHostPath(arg string) (string, error) {
	slashed := strings.ReplaceAll(arg, `\`, "/")
	if strings.HasPrefix(slashed, "/") && !strings.HasPrefix(slashed, "//") {
		cwd, err := hostCwd()
		if err != nil {
			return "", err
		}
		slashed = pathRoot(strings.ReplaceAll(cwd, `\`, "/")) + slashed
	}
	if _, _, ok := splitDrive(slashed); !ok && !strings.HasPrefix(slashed, "/") {
		// Join on the host side, so that cleaning the result stops `..` at
		// the drive root.
		base, rest, err := relativeBase(slashed)
		if err != nil {
			return "", err
		}
		slashed = strings.ReplaceAll(base, `\`, "/") + "/" + rest
	}
	if drive, rest, ok := splitDrive(trimExtendedPrefix(slashed)); ok {
		return "/mnt/" + drive + path.Clean("/"+rest), nil
	}
	// UNC (//server/share/...), which includes paths resolved against a UNC
	// working directory; nothing we can map.
	return slashed, nil
}

// IsDriveAbsolute reports whether the host path starts with a drive and a
// slash or backslash, as in `C:\data`, `C:/data`, `c:\` or the extended-length
// `\\?\C:\data`. TranslateHostPath resolves such a path without a working
// directory.
func IsDriveAbsolute(hostPath string) bool {
	_, _, ok := splitDrive(trimExtendedPrefix(strings.ReplaceAll(hostPath, `\`, "/")))
	return ok
}

// CutVolumeSource drops the leading colons of a volume spec and cuts the
// source at the next colon, as nerdctl's splitVolumeSpec does, except that
// the colon after a leading drive stays in the source, as in `C:\src` or the
// extended-length `\\?\C:\src`. found reports whether a colon ended the
// source, and rest is what follows it.
func CutVolumeSource(spec string) (source, rest string, found bool) {
	spec = strings.TrimLeft(spec, ":")
	driveEnd := len("c:")
	if strings.HasPrefix(strings.ReplaceAll(spec, `\`, "/"), extendedPrefix) {
		driveEnd += len(extendedPrefix)
	}
	drive := ""
	if len(spec) >= driveEnd && spec[driveEnd-1] == ':' && isDriveLetter(spec[driveEnd-2]) {
		drive, spec = spec[:driveEnd], spec[driveEnd:]
	}
	source, rest, found = strings.Cut(spec, ":")
	return drive + source, rest, found
}

// extendedPrefix is the `\\?\` prefix of an extended-length path such as
// `\\?\C:\data`, after the backslashes are turned into slashes.
const extendedPrefix = "//?/"

// trimExtendedPrefix drops extendedPrefix from a slashed extended-length
// drive path, so `//?/C:/data` becomes `C:/data`. It returns any other path
// unchanged, such as `//?/UNC/server/share`.
func trimExtendedPrefix(slashed string) string {
	if rest, ok := strings.CutPrefix(slashed, extendedPrefix); ok {
		if _, _, isDrive := splitDrive(rest); isDrive {
			return rest
		}
	}
	return slashed
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

// pathRoot returns the drive of the absolute slashed path (`c:`), or its
// first two components after a leading `//` (`//server/share`).
func pathRoot(slashed string) string {
	if drive, _, ok := splitDrive(slashed); ok {
		return drive + ":"
	}
	if rest, ok := strings.CutPrefix(slashed, "//"); ok {
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) >= 2 {
			return "//" + parts[0] + "/" + parts[1]
		}
	}
	return ""
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
