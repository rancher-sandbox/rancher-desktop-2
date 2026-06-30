// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strconv"

	"github.com/pbnjay/memory"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	servicecontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

const (
	// HostInfoConfigMapName is the name of the ConfigMap that stores host hardware limits.
	HostInfoConfigMapName = "rdd-host-info"
	// HostInfoCPUsKey is the ConfigMap data key for the host CPU count.
	HostInfoCPUsKey = "cpus"
	// HostInfoMemoryKey is the ConfigMap data key for the host total memory in bytes.
	HostInfoMemoryKey = "memory"
)

// HostInfo holds the detected host hardware limits used to validate VM resource requests.
type HostInfo struct {
	// CPUs is the number of logical CPUs on the host.
	CPUs int
	// Memory is the total host memory in bytes.
	Memory int64
}

// DetectHostInfo reads the host CPU count and total memory.
func DetectHostInfo() HostInfo {
	return HostInfo{
		CPUs:   goruntime.NumCPU(),
		Memory: int64(memory.TotalMemory()),
	}
}

// CreateOrUpdateHostInfoConfigMap writes the host hardware limits to the
// rdd-system/rdd-host-info ConfigMap so the GUI and other consumers can read
// the valid range for VM resource settings without inspecting the host directly.
func CreateOrUpdateHostInfoConfigMap(ctx context.Context, c client.Client) error {
	log := logf.FromContext(ctx)
	info := DetectHostInfo()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HostInfoConfigMapName,
			Namespace: servicecontrollers.RDDSystemNamespace,
		},
		Data: map[string]string{
			HostInfoCPUsKey:   strconv.Itoa(info.CPUs),
			HostInfoMemoryKey: strconv.FormatInt(info.Memory, 10),
		},
	}

	existing := &corev1.ConfigMap{}
	err := c.Get(ctx, client.ObjectKeyFromObject(cm), existing)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, cm); err != nil {
			return fmt.Errorf("failed to create host-info ConfigMap: %w", err)
		}
		log.Info("Created host-info ConfigMap", "cpus", info.CPUs, "memory", info.Memory)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get host-info ConfigMap: %w", err)
	}

	existing.Data = cm.Data
	if err := c.Update(ctx, existing); err != nil {
		return fmt.Errorf("failed to update host-info ConfigMap: %w", err)
	}
	log.Info("Updated host-info ConfigMap", "cpus", info.CPUs, "memory", info.Memory)
	return nil
}
