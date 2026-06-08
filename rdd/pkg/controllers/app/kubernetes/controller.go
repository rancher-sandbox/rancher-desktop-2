// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package kubernetes registers the Kubernetes context controller. The
// controller probes the k3s API server and manages the
// rancher-desktop-{instance} context in ~/.kube/config whenever
// spec.kubernetes.enabled is true and the VM is running.
package kubernetes

import (
	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/kubernetes/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

func init() {
	base.RegisterController(&controller{})
}

type controller struct{}

var _ base.Controller = &controller{}

func (c *controller) GetName() string {
	return appv1alpha1.KubernetesControllerName
}

func (c *controller) GetAPIGroup() string {
	// No additional CRD group beyond app; return empty to signal that no
	// extra scheme registration is needed.
	return ""
}

func (c *controller) GetCRDData() string {
	return ""
}

func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := appv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	r := &controllers.KubernetesReconciler{
		Client:                 mgr.GetClient(),
		K3sConfigPath:          instance.K3sConfig(),
		InstanceKubeConfigPath: instance.KubeConfig(),
	}
	return r.SetupWithManager(mgr)
}
