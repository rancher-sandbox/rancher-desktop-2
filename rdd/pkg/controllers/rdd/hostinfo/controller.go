// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package hostinfo registers the HostInfo controller, which maintains a
// cluster-scoped singleton exposing host hardware limits in its Status.
package hostinfo

import (
	"context"
	_ "embed"

	ctrl "sigs.k8s.io/controller-runtime"

	rddv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/rdd/hostinfo/controllers"
)

func init() {
	base.RegisterController(&controller{})
}

// ControllerName is the name of this controller.
const ControllerName = "hostinfo"

// APIGroup is the API group this controller belongs to.
const APIGroup = "rdd"

//go:embed crd.yaml
var hostInfoCRD string

type controller struct{}

var _ base.Controller = &controller{}

func (c *controller) GetName() string     { return ControllerName }
func (c *controller) GetAPIGroup() string { return APIGroup }
func (c *controller) GetCRDData() string  { return hostInfoCRD }

// RegisterWithManager registers the CRD scheme and the HostInfo reconciler.
func (c *controller) RegisterWithManager(_ context.Context, mgr ctrl.Manager) error {
	if err := rddv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	return (&controllers.HostInfoReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr)
}
