// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// This file rewrites Windows host paths to the /mnt/<drive> locations where
// the WSL2 guest automounts them. The functions work on plain strings
// instead of path/filepath, so they compile and unit-test on every
// platform; only cmd/rdd decides to call them (on Windows).

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
)

// hostCwd returns the host working directory used to resolve relative
// paths; tests replace it.
var hostCwd = os.Getwd

// TranslateHostPath rewrites one host path to where the guest mounts it,
// e.g. `C:\Users\jan\app` to `/mnt/c/Users/jan/app`. Values without a drive
// letter (UNC paths, absolute POSIX-style paths) pass through unchanged.
func TranslateHostPath(arg string) (string, error) {
	slashed := strings.ReplaceAll(arg, `\`, "/")
	if drive, rest, ok := splitDrive(slashed); ok {
		return "/mnt/" + drive + path.Clean("/"+rest), nil
	}
	if strings.HasPrefix(slashed, "/") {
		// UNC (//server/share) or POSIX-style; nothing we can map.
		return slashed, nil
	}
	// A relative path resolves against the working directory.
	cwd, err := hostCwd()
	if err != nil {
		return "", err
	}
	base, err := TranslateHostPath(cwd)
	if err != nil {
		return "", err
	}
	return path.Join(base, slashed), nil
}

// splitDrive splits `c:/Users/jan` into "c" and "/Users/jan". Only
// drive-absolute paths match; drive-relative ones (`c:foo`) do not.
func splitDrive(slashed string) (drive, rest string, ok bool) {
	if len(slashed) < 3 || slashed[1] != ':' || slashed[2] != '/' {
		return "", "", false
	}
	letter := slashed[0]
	if (letter < 'a' || letter > 'z') && (letter < 'A' || letter > 'Z') {
		return "", "", false
	}
	return strings.ToLower(slashed[:1]), slashed[2:], true
}

// volumeArgHandler handles the argument of `nerdctl run --volume=...`.
func volumeArgHandler(arg string) (string, []cleanupFunc, error) {
	// Valid arguments are:
	// <host path>:<container path>
	// <host path>:<container path>:rw
	// <host path>:<container path>:ro
	// Because we only have Linux containers, and this is for Windows, we
	// need not handle a bare <path> for identical host and container paths.
	cleanArg := arg
	readWrite := ""
	if strings.HasSuffix(arg, ":ro") || strings.HasSuffix(arg, ":rw") {
		readWrite = arg[len(arg)-3:]
		cleanArg = arg[:len(arg)-3]
	}
	// For now, assume the container path contains no colons.
	colonIndex := strings.LastIndex(cleanArg, ":")
	if colonIndex < 0 {
		return "", nil, fmt.Errorf("invalid volume mount: %s does not contain : separator", arg)
	}
	hostPath := cleanArg[:colonIndex]
	containerPath := cleanArg[colonIndex+1:]
	guestPath, err := TranslateHostPath(hostPath)
	if err != nil {
		return "", nil, fmt.Errorf("could not get volume host path for %s: %w", arg, err)
	}
	return guestPath + ":" + containerPath + readWrite, nil, nil
}

// mountArgHandler handles the argument of `nerdctl run --mount=...`.
func mountArgHandler(arg string) (string, []cleanupFunc, error) {
	var chunks [][]string
	isBind := false
	for _, chunk := range strings.Split(arg, ",") {
		parts := strings.SplitN(chunk, "=", 2)
		if len(parts) != 2 {
			// A chunk with no value, e.g. --mount=...,readonly,...
			chunks = append(chunks, []string{chunk})
			continue
		}
		if parts[0] == "type" && parts[1] == "bind" {
			isBind = true
		}
		chunks = append(chunks, parts)
	}
	if !isBind {
		// Not a bind mount; nothing to rewrite.
		return arg, nil, nil
	}
	for _, chunk := range chunks {
		if len(chunk) != 2 || (chunk[0] != "source" && chunk[0] != "src") {
			continue
		}
		guestPath, err := TranslateHostPath(chunk[1])
		if err != nil {
			return "", nil, err
		}
		chunk[1] = guestPath
	}
	var parts []string
	for _, chunk := range chunks {
		parts = append(parts, strings.Join(chunk, "="))
	}
	return strings.Join(parts, ","), nil, nil
}

// filePathArgHandler handles arguments that take a path for input.
func filePathArgHandler(arg string) (string, []cleanupFunc, error) {
	result, err := TranslateHostPath(arg)
	if err != nil {
		return "", nil, err
	}
	return result, nil, nil
}

// outputPathArgHandler handles arguments that take a path to write to. On
// Windows the guest writes through the /mnt/<drive> mount directly, so this
// matches the input handling.
func outputPathArgHandler(arg string) (string, []cleanupFunc, error) {
	return filePathArgHandler(arg)
}

// builderCacheArgHandler handles `nerdctl builder build --cache-from=`,
// `--cache-to=`, `--output=`, and `--secret=`.
func builderCacheArgHandler(arg string) (string, []cleanupFunc, error) {
	var cleanups []cleanupFunc

	// The arg is comma-separated, with `src=` values as inputs and `dest=`
	// values as outputs; everything else passes through.
	var parts []string
	for _, part := range strings.Split(arg, ",") {
		handler := filePathArgHandler
		switch {
		case strings.HasPrefix(part, "src="):
			// handled below
		case strings.HasPrefix(part, "dest="):
			handler = outputPathArgHandler
		default:
			parts = append(parts, part)
			continue
		}
		key, value, _ := strings.Cut(part, "=")
		fixedPath, newCleanups, err := handler(value)
		cleanups = append(cleanups, newCleanups...)
		if err != nil {
			return "", nil, errors.Join(err, runCleanups(cleanups))
		}
		parts = append(parts, key+"="+fixedPath)
	}
	return strings.Join(parts, ","), cleanups, nil
}

// buildContextArgHandler handles `nerdctl builder build --build-context=`.
func buildContextArgHandler(arg string) (string, []cleanupFunc, error) {
	// The arg is CSV of key=value pairs; each value is either a URN with a
	// prefix from urnPrefixes, or a filesystem path.
	urnPrefixes := []string{"https://", "http://", "docker-image://", "target:", "oci-layout://"}
	parts, err := csv.NewReader(strings.NewReader(arg)).Read()
	if err != nil {
		return "", nil, err
	}
	var resultParts []string
	for _, part := range parts {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return "", nil, fmt.Errorf("failed to parse context value %q (expected key=value)", part)
		}
		matchesPrefix := func(prefix string) bool {
			return strings.HasPrefix(v, prefix)
		}
		if !slices.ContainsFunc(urnPrefixes, matchesPrefix) {
			v, err = TranslateHostPath(v)
			if err != nil {
				return "", nil, err
			}
		}
		resultParts = append(resultParts, k+"="+v)
	}
	var result bytes.Buffer
	writer := csv.NewWriter(&result)
	if err := writer.Write(resultParts); err != nil {
		return "", nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(result.String()), nil, nil
}

// argHandlers is the table of argument rewrite functions.
var argHandlers = argHandlersType{
	volumeArgHandler:       volumeArgHandler,
	filePathArgHandler:     filePathArgHandler,
	outputPathArgHandler:   outputPathArgHandler,
	mountArgHandler:        mountArgHandler,
	builderCacheArgHandler: builderCacheArgHandler,
	buildContextArgHandler: buildContextArgHandler,
}
