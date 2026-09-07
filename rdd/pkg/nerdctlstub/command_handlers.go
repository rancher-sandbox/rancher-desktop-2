// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package nerdctlstub

// This file contains handlers for the positional arguments of specific
// commands, ported from Rancher Desktop 1.x.

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var urlLikeRE = regexp.MustCompile(`^[^:/]*://`)

// fileOrURLOrStdin handles arguments of kind `file|URL|-`. It returns a
// rewritten path (or the argument as-is), any cleanups, and errors.
func fileOrURLOrStdin(input string, argHandlers argHandlersType) (string, []cleanupFunc, error) {
	if input == "-" || urlLikeRE.MatchString(input) {
		return input, nil, nil
	}
	newPath, cleanups, err := argHandlers.filePathArgHandler(input)
	if err != nil {
		return input, nil, errors.Join(err, runCleanups(cleanups))
	}
	return newPath, cleanups, nil
}

// builderBuildHandler handles `nerdctl builder build`.
func builderBuildHandler(_ *commandDefinition, args []string, argHandlers argHandlersType) (*ParsedArgs, error) {
	// nerdctl builder build [flags] PATH
	// The first argument is the directory to build; the rest are ignored.
	if len(args) < 1 {
		// nerdctl will report the missing argument.
		return &ParsedArgs{Args: args}, nil
	}
	newPath, cleanups, err := fileOrURLOrStdin(args[0], argHandlers)
	if err != nil {
		return nil, err
	}
	return &ParsedArgs{Args: append([]string{newPath}, args[1:]...), cleanup: cleanups}, nil
}

// hostPathResult says which of the two `nerdctl container cp` positional
// arguments is the host path that must be rewritten.
type hostPathResult int

const (
	hostPathUnknown hostPathResult = iota
	hostPathCurrent
	hostPathOther
	hostPathNeither
)

// containerCopyHandler handles `nerdctl container cp`.
func containerCopyHandler(_ *commandDefinition, args []string, argHandlers argHandlersType) (*ParsedArgs, error) {
	var resultArgs []string
	var cleanups []cleanupFunc
	var paths []string

	// Positional arguments of `nerdctl container cp` are all paths, whether
	// inside the container or outside.
	for _, arg := range args {
		if arg == "-" || !strings.HasPrefix(arg, "-") {
			// "-" (stdin/stdout) counts as a path.
			paths = append(paths, arg)
		} else {
			resultArgs = append(resultArgs, arg)
		}
	}

	if len(paths) != 2 {
		// We should have exactly one source and one destination.
		err := fmt.Errorf("accepts 2 args, received %d", len(paths))
		return nil, errors.Join(err, runCleanups(cleanups))
	}

	hostPathDeterminerFuncs := []func(i int, p string) hostPathResult{
		func(_ int, p string) hostPathResult {
			if p == "-" {
				// If one argument is "-", the other must be a container
				// path, so neither needs to be modified.
				return hostPathNeither
			}
			return hostPathUnknown
		},
		func(_ int, p string) hostPathResult {
			colon := strings.Index(p, ":")
			if colon < 1 {
				// Without any colon (or starting with one, which is
				// invalid), this must not be a container path, so it is the
				// host path.
				return hostPathCurrent
			}
			return hostPathUnknown
		},
		func(_ int, p string) hostPathResult {
			colon := strings.Index(p, ":")
			if colon > 1 {
				// Multiple characters before the first colon make this a
				// container path (name:/path), so the other one is the
				// host path.
				return hostPathOther
			}
			return hostPathUnknown
		},
		func(i int, p string) hostPathResult {
			if strings.Index(p, ":") != 1 {
				// One of the previous functions should have decided.
				panic(fmt.Sprintf("expected path %q to start with a character followed by a colon", p))
			}
			if i != 0 {
				panic("should not reach this on the second path")
			}
			// Fall back: treat the first element as the container path.
			return hostPathOther
		},
	}

functionLoop:
	for _, f := range hostPathDeterminerFuncs {
		for i, p := range paths {
			result := f(i, p)
			hostPathIndex := i
			switch result {
			case hostPathNeither:
				//nolint:gocritic // We break the loop once we are done appending
				resultArgs = append(resultArgs, paths...)
				break functionLoop
			case hostPathUnknown:
				continue
			case hostPathOther:
				hostPathIndex = 1 - i
			}

			// We found the host path to rewrite; modify it in place.
			newPath, newCleanups, err := argHandlers.filePathArgHandler(paths[hostPathIndex])
			cleanups = append(cleanups, newCleanups...)
			if err != nil {
				return nil, errors.Join(err, runCleanups(cleanups))
			}
			paths[hostPathIndex] = newPath
			//nolint:gocritic // We break the loop once we are done appending
			resultArgs = append(resultArgs, paths...)
			break functionLoop
		}
	}

	return &ParsedArgs{Args: resultArgs, cleanup: cleanups}, nil
}
