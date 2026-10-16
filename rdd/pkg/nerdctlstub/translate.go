// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// This file rewrites the host paths in nerdctl arguments to the /mnt/<drive>
// locations where the WSL2 guest automounts them. cmd/rdd calls it only on
// Windows.

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/guestexec"
)

// namedVolume matches the volume sources that nerdctl takes for volume names
// instead of host paths; it copies isNamedVolume in nerdctl's pkg/mountutil.
var namedVolume = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

// volumeArgHandler handles the argument of `nerdctl run --volume=...`.
func volumeArgHandler(arg string) (string, []cleanupFunc, error) {
	// Valid arguments are:
	// <host path or volume name>:<container path>
	// <host path or volume name>:<container path>:rw
	// <host path or volume name>:<container path>:ro
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
	if namedVolume.MatchString(hostPath) {
		return arg, nil, nil
	}
	guestPath, err := guestexec.TranslateHostPath(hostPath)
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
		guestPath, err := guestexec.TranslateHostPath(chunk[1])
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
	result, err := guestexec.TranslateHostPath(arg)
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
			v, err = guestexec.TranslateHostPath(v)
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
