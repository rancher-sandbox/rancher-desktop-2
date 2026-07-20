// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	k3sversionscontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/k3sversions/controllers"
)

const (
	minMemoryBytes = 2 * 1024 * 1024 * 1024 // 2 GiB
	// maxDefaultMemoryBytes caps the memory the defaulter picks. The defaulter
	// uses 25% of host memory but never more than this (matching RD1 settings).
	maxDefaultMemoryBytes = 6 * 1024 * 1024 * 1024 // 6 GiB
)

// AppValidator validates App resources via admission webhook.
type AppValidator struct {
	supportedK8sVersions map[string]string
	hostInfo             HostInfo
}

// NewAppValidator returns a new AppValidator using the given k3s channel
// information.  hostInfo provides the upper bounds for CPU and memory validation.
func NewAppValidator(versions k3sversionscontrollers.K3sVersions, hostInfo HostInfo) *AppValidator {
	return &AppValidator{supportedK8sVersions: versions.Versions, hostInfo: hostInfo}
}

// ValidateCreate validates a new App resource.
func (v *AppValidator) ValidateCreate(_ context.Context, obj *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(obj)
}

// ValidateUpdate validates an updated App resource.
func (v *AppValidator) ValidateUpdate(_ context.Context, _, newObj *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return v.validate(newObj)
}

// ValidateDelete is a no-op; App deletion is governed by the cleanup finalizer.
func (v *AppValidator) ValidateDelete(_ context.Context, _ *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	return nil, nil
}

func (v *AppValidator) validate(app *v1alpha1.App) (ctrlwebhookadmission.Warnings, error) {
	k8s := app.Spec.Kubernetes
	if k8s.Enabled && k8s.Version == "" {
		return nil, errors.New("spec.kubernetes.version must not be empty when spec.kubernetes.enabled is true")
	}

	if k8s.Version != "" {
		if _, ok := v.supportedK8sVersions[k8s.Version]; !ok {
			return nil, fmt.Errorf("spec.kubernetes.version %q is not supported; see the bundled k3s-versions.json for valid versions", k8s.Version)
		}
	}

	if err := v.validateVirtualMachine(app.Spec.VirtualMachine); err != nil {
		return nil, err
	}

	return nil, nil
}

func (v *AppValidator) validateVirtualMachine(vm v1alpha1.VirtualMachineSpec) error {
	if vm.CPUs < 0 {
		return fmt.Errorf("spec.virtualMachine.cpus %d must not be negative", vm.CPUs)
	}
	// cpus == 0 only reaches the validator when the mutating webhook is bypassed;
	// normally AppDefaulter fills in a concrete count first. Only an explicit
	// positive request is checked against the host.
	if vm.CPUs > 0 && v.hostInfo.CPUs > 0 && vm.CPUs > v.hostInfo.CPUs {
		return fmt.Errorf("spec.virtualMachine.cpus %d exceeds the host CPU count of %d", vm.CPUs, v.hostInfo.CPUs)
	}

	if vm.Memory != nil {
		memBytes := vm.Memory.Value()
		if memBytes < minMemoryBytes {
			minQ := resource.NewQuantity(minMemoryBytes, resource.BinarySI)
			return fmt.Errorf("spec.virtualMachine.memory %v is less than the minimum of %v", vm.Memory, minQ)
		}
		if v.hostInfo.Memory > 0 && memBytes > v.hostInfo.Memory {
			maxQ := resource.NewQuantity(v.hostInfo.Memory, resource.BinarySI)
			return fmt.Errorf("spec.virtualMachine.memory %v exceeds the host memory of %v", vm.Memory, maxQ)
		}
	}

	return nil
}
