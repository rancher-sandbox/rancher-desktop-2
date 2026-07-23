// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

// Package hostswitch runs the WSL2 host-switch virtual network on Windows. On
// other platforms it is a no-op: the host-switch bridges networking between a
// Hyper-V VM and the Windows host via AF_VSOCK, which only exists on Windows.
package hostswitch

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/lima-vm/lima/v2/pkg/limatype"
)

// Run is a no-op on non-Windows platforms. The host-switch virtual network is
// only needed for WSL2 instances, which require AF_VSOCK to bridge networking
// between the Hyper-V VM and the Windows host.
func Run(_ context.Context, _ logr.Logger, _ *limatype.Instance) error { return nil }
