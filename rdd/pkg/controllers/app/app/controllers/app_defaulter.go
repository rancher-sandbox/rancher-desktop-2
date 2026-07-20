// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	k3sversionscontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/k3sversions/controllers"
)

const (
	// defaultK8sChannel is the channel used when Kubernetes is enabled without a version.
	defaultK8sChannel = "stable"
	// defaultVMCPUs is the RD1 default cpu count the admission controller writes
	// when spec.virtualMachine.cpus is unset (0) and RDD_VM_CPUS is not set.
	defaultVMCPUs = 2
	// vmCPUsEnv overrides the default VM cpu count for CI. An explicit
	// spec.virtualMachine.cpus still takes precedence over this override.
	vmCPUsEnv = "RDD_VM_CPUS"
)

// AppDefaulter resolves channel aliases in App resources via a mutating
// admission webhook. It runs before the validating webhook, so an alias such
// as "stable" or "latest" becomes a concrete version that AppValidator accepts.
type AppDefaulter struct {
	channels map[string]string
	hostInfo HostInfo
}

// NewAppDefaulter returns a new AppDefaulter using the given k3s channel
// information.  hostInfo provides the host memory used to default
// spec.virtualMachine.memory.
func NewAppDefaulter(versions k3sversionscontrollers.K3sVersions, hostInfo HostInfo) *AppDefaulter {
	return &AppDefaulter{channels: versions.Channels, hostInfo: hostInfo}
}

var _ ctrlwebhookadmission.Defaulter[*v1alpha1.App] = &AppDefaulter{}

// Default resolves a channel alias in spec.kubernetes.version to a concrete
// version and fills in the default VM cpu count and memory. All run before the
// validating webhook, so an alias such as "stable" becomes a concrete version
// and cpus/memory become concrete values that AppValidator can accept or reject.
func (d *AppDefaulter) Default(_ context.Context, app *v1alpha1.App) error {
	d.defaultKubernetesVersion(&app.Spec.Kubernetes)
	return d.defaultVirtualMachine(&app.Spec.VirtualMachine)
}

// defaultKubernetesVersion resolves a channel alias in k8s.Version to a
// concrete version. When Kubernetes is enabled without a version, it uses the
// "stable" channel. A version that matches no channel is left unchanged for
// AppValidator to accept or reject.
func (d *AppDefaulter) defaultKubernetesVersion(k8s *v1alpha1.KubernetesSpec) {
	version := k8s.Version
	if version == "" {
		if !k8s.Enabled {
			return
		}
		version = defaultK8sChannel
	}
	if resolved, ok := d.channels[strings.TrimPrefix(version, "v")]; ok {
		k8s.Version = resolved
	}
}

// defaultVirtualMachine writes concrete cpu and memory values into an unset
// spec.virtualMachine. Keeping this in the admission controller lets the CLI and
// the reconciler treat cpus/memory as plain values instead of special-casing the
// zero value.
func (d *AppDefaulter) defaultVirtualMachine(vm *v1alpha1.VirtualMachineSpec) error {
	if err := defaultVMCPUCount(vm, d.hostInfo); err != nil {
		return err
	}
	return defaultVMMemory(vm, d.hostInfo)
}

// defaultVMCPUCount writes a concrete cpu count into an unset (0)
// spec.virtualMachine.cpus. RDD_VM_CPUS overrides the built-in default for CI;
// an explicit cpus wins over both. The resolved default is clamped to the host
// CPU count so the mutating webhook never writes a value the validating webhook
// would reject (e.g. the default 2 on a single-vCPU host).
func defaultVMCPUCount(vm *v1alpha1.VirtualMachineSpec, hostInfo HostInfo) error {
	if vm.CPUs != 0 {
		return nil
	}
	cpus := defaultVMCPUs
	if val := os.Getenv(vmCPUsEnv); val != "" {
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid %s value %q: want a positive integer", vmCPUsEnv, val)
		}
		cpus = n
	}
	if hostInfo.CPUs > 0 && cpus > hostInfo.CPUs {
		cpus = hostInfo.CPUs
	}
	vm.CPUs = cpus
	return nil
}

// defaultVMMemory writes a concrete memory value into an unset
// spec.virtualMachine.memory. Following RD1 (not Lima) settings, it picks 25% of
// host memory, clamped to [minMemoryBytes, maxDefaultMemoryBytes]. A host with
// less than minMemoryBytes cannot satisfy the validator's minimum, so it returns
// a distinct error rather than writing a default the validating webhook would
// reject as exceeding host memory.
func defaultVMMemory(vm *v1alpha1.VirtualMachineSpec, hostInfo HostInfo) error {
	if vm.Memory != nil {
		return nil
	}
	if hostInfo.Memory > 0 && hostInfo.Memory < minMemoryBytes {
		minQ := resource.NewQuantity(minMemoryBytes, resource.BinarySI)
		hostQ := resource.NewQuantity(hostInfo.Memory, resource.BinarySI)
		return fmt.Errorf("host memory %v is below the %v minimum", hostQ, minQ)
	}
	memBytes := hostInfo.Memory / 4
	if memBytes > maxDefaultMemoryBytes {
		memBytes = maxDefaultMemoryBytes
	}
	if memBytes < minMemoryBytes {
		memBytes = minMemoryBytes
	}
	vm.Memory = resource.NewQuantity(memBytes, resource.BinarySI)
	return nil
}
