// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package engine registers the engine controller. The engine controller
// mirrors Docker container engine state (containers, images, volumes)
// into `Container`, `Image`, and `Volume` resources in the
// containers.rancherdesktop.io API group, and forwards user-initiated
// deletions back to the Docker engine.
package engine

import (
	"maps"
	"net/http"
	"slices"

	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/engine/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(newController())
}

// apiGroup is "containers" because the reconciler watches
// Container, Image, and Volume from that group and needs
// their CRDs at startup. Grouping engine with its dependencies
// keeps --controllers selections from splitting the two apart.
const apiGroup = "containers"

type controller struct {
	reconciler   *controllers.EngineReconciler
	passthroughs map[string]http.Handler
}

func newController() base.Controller {
	c := &controller{}
	c.passthroughs = map[string]http.Handler{
		"logs": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c.reconciler.HandleLogs(w, r)
		}),
	}
	return c
}

var (
	_ base.Controller            = &controller{}
	_ base.PassthroughController = &controller{}
)

func (c *controller) GetName() string {
	return appv1alpha1.EngineControllerName
}

func (c *controller) GetAPIGroup() string {
	return apiGroup
}

func (c *controller) GetCRDData() string {
	return ""
}

func (c *controller) GetPassthroughEndpoints() []string {
	return slices.Sorted(maps.Keys(c.passthroughs))
}

func (c *controller) GetPassthroughHandler(endpoint string) http.Handler {
	if c.reconciler == nil {
		return nil
	}
	return c.passthroughs[endpoint]
}

func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	if err := appv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := containersv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	c.reconciler = &controllers.EngineReconciler{
		Client: mgr.GetClient(),
	}
	return c.reconciler.SetupWithManager(mgr)
}
