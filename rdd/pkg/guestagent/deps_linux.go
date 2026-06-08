// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package guestagent

// Blank import ensures go mod tidy retains the guest agent's
// Linux-only transitive dependencies in go.sum.
// Without this, go mod tidy on macOS would strip them, breaking the
// Makefile's cross-compilation build of lima-guestagent.
import _ "github.com/lima-vm/lima/v2/pkg/guestagent"
