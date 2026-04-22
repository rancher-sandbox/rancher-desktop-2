// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package exit defines distinct CLI exit codes so callers can tell apart
// admission rejection, wait timeout, and generic internal errors.
package exit

import (
	"context"
	"errors"
)

// Predefined exit codes. 0 and 1 are the defaults handled by main; 2 is
// reserved for cobra usage errors, so named codes start at 3.
const (
	// CodeRejected indicates the API server's admission controller rejected the request.
	CodeRejected = 3

	// CodeTimeout indicates the wait deadline expired before the desired state was reached.
	CodeTimeout = 4
)

// Error is an error that carries a process exit code for main to return.
type Error struct {
	Code int
	Err  error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap exposes the wrapped error to errors.Is and errors.As.
func (e *Error) Unwrap() error {
	return e.Err
}

// Rejected wraps err with [CodeRejected].
func Rejected(err error) *Error {
	return &Error{Code: CodeRejected, Err: err}
}

// Timeout wraps err with [CodeTimeout].
func Timeout(err error) *Error {
	return &Error{Code: CodeTimeout, Err: err}
}

// Classify wraps err with [CodeTimeout] when err wraps
// [context.DeadlineExceeded]. Already-classified errors and unrelated
// errors pass through unchanged, so callers may Classify repeatedly.
func Classify(err error) error {
	var exitErr *Error
	if errors.As(err, &exitErr) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return Timeout(err)
	}
	return err
}
