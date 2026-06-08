// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

//go:build !windows

package controllers

import (
	"context"

	"github.com/lima-vm/lima/v2/pkg/limatype"
)

// hostSwitchPlatform is empty on non-Windows platforms. On Windows, it holds
// the mutex and state map for host-switch goroutines (see hostswitch_windows.go).
type hostSwitchPlatform struct{}

// initHostSwitch is a no-op on non-Windows platforms.
func (hostSwitchPlatform) initHostSwitch() {}

// startHostSwitch is a no-op on non-Windows platforms. The host-switch virtual
// network is only needed for WSL2 instances, which require AF_VSOCK to bridge
// networking between the Hyper-V VM and the Windows host.
func (r *LimaVMReconciler) startHostSwitch(_ context.Context, _ string, _ *limatype.Instance) {}

// stopHostSwitch is a no-op on non-Windows platforms.
func (r *LimaVMReconciler) stopHostSwitch(_ string) {}
