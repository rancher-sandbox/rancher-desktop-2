// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package demo registers the Demo controller. The Demo controller exists for
// development and testing; it exercises the controller framework with a minimal
// cluster-scoped singleton and passthrough HTTP endpoints. It will be removed
// before the first release.
package demo

import (
	_ "embed"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"

	"github.com/gorilla/websocket"

	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/demo/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

func init() {
	base.RegisterController(newController())
}

// ControllerName is the name of this controller.
const ControllerName = "demo"

// APIGroup is the API group this controller belongs to.
const APIGroup = "app"

//go:embed crd.yaml
var demoCRD string

// controller implements the base.Controller interface for demo.
type controller struct {
	passthroughs map[string]http.Handler
}

func newController() base.Controller {
	c := &controller{}
	c.passthroughs = map[string]http.Handler{
		"hello":     http.HandlerFunc(c.handleHello),
		"websocket": http.HandlerFunc(c.handleWebSocket),
	}
	return c
}

// Verify that controller implements base.Controller interface.
var (
	_ base.Controller            = &controller{}
	_ base.PassthroughController = &controller{}
)

// GetName returns the controller name.
func (c *controller) GetName() string {
	return ControllerName
}

// GetAPIGroup returns the API group this controller belongs to.
func (c *controller) GetAPIGroup() string {
	return APIGroup
}

// GetCRDData returns the embedded CRD YAML data.
func (c *controller) GetCRDData() string {
	return demoCRD
}

func (c *controller) GetPassthroughEndpoints() []string {
	return slices.Collect(maps.Keys(c.passthroughs))
}

func (c *controller) GetPassthroughHandler(endpoint string) http.Handler {
	return c.passthroughs[endpoint]
}

func (c *controller) handleHello(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "Hello, world!\n")
	for k, v := range r.Header {
		_, _ = fmt.Fprintf(w, "%s = %q\n", k, v)
	}
	_, _ = fmt.Fprintf(w, "Path = %q\n", r.URL.Path)
}

func (c *controller) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	log := ctrl.LoggerFrom(r.Context())
	upgrader := &websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.V(5).Info("Failed to upgrade to WebSocket", "error", err)
		http.Error(w, "Failed to upgrade to WebSocket: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer conn.Close()

	err = conn.WriteMessage(websocket.TextMessage, []byte("hello from websocket\n"))
	if err != nil {
		log.V(5).Info("Failed to write WebSocket message", "error", err)
	}
}

// RegisterWithManager implements the complete controller registration for both embedded and external modes.
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	// Register the CRD types with the scheme
	if err := v1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	// Create and set up the controller with the manager
	return (&controllers.DemoReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder(ControllerName + "-controller"),
	}).SetupWithManager(mgr)
}
