// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package mock

import (
	"context"
	_ "embed"
	"maps"
	"net/http"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	rddv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/rdd/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/app/k3sversions/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

const (
	// The reconcilers will trigger on update of a namespace with this name.
	mockNamespaceName = "rdd-mocks"
	// The name of the Kubernetes namespace in which the resources will be created.
	apiNamespace = "rancher-desktop"
	// The mock controller creates all items in this (container) namespace.
	containerNamespace = "buildkit"

	apiGroup           = "mock"
	controllerName     = "mock"
	controllerLongName = controllerName + "-controller"
)

//go:embed crd.yaml
var controllerCRD string

func init() {
	base.RegisterController(newController())
}

// controller that creates mock data when started.
// This creates a "rdd-mocks" namespace to own the created resources, and
// reconcilers that create mocked resources based on the namespace.
type controller struct {
	mgr             ctrl.Manager
	webhookPort     int
	webhookManagers []base.WebhookManager
	passthroughs    map[string]http.Handler
}

func newController() *controller {
	c := &controller{}
	c.passthroughs = map[string]http.Handler{
		"logs": http.HandlerFunc(c.handleLogs),
	}
	return c
}

var (
	_ base.Controller            = &controller{}
	_ base.WebhookController     = &controller{}
	_ base.PassthroughController = &controller{}
)

func (c *controller) GetName() string {
	return controllerName
}

func (c *controller) GetAPIGroup() string {
	return apiGroup
}

func (c *controller) SetWebhookPort(port int) {
	c.webhookPort = port
}

func (c *controller) GetCRDData() string {
	return controllerCRD
}

func (c *controller) GetPassthroughEndpoints() []string {
	return slices.Collect(maps.Keys(c.passthroughs))
}

func (c *controller) GetPassthroughHandler(endpoint string) http.Handler {
	return c.passthroughs[endpoint]
}

func (c *controller) setupReconciler(ctx context.Context, mgr ctrl.Manager) error {
	log := mgr.GetLogger()

	log.Info("Setting up Mock KubeNamespaceReconciler")
	err := (&kubeNamespaceReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder(controllerLongName),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock AppReconciler")
	err = (&appReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock ContainerNamespaceReconciler")
	err = (&containerNamespaceReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder(controllerLongName),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock ContainerReconciler")
	err = (&containerReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder(controllerLongName),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock ImageReconciler")
	err = (&imageReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder(controllerLongName),
	}).SetupWithManager(ctx, mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock VolumeReconciler")
	err = (&volumeReconciler{
		Client:   mgr.GetClient(),
		Recorder: mgr.GetEventRecorder(controllerLongName),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up K3s versions reconciler")
	err = (&controllers.K3sVersionsReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	log.Info("Setting up Mock HostInfoReconciler")
	err = (&hostInfoReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr)
	if err != nil {
		return err
	}

	return nil
}

func (c *controller) createNamespace(ctx context.Context, mgr ctrl.Manager) error {
	client := mgr.GetClient()
	namespacedName := types.NamespacedName{
		Name: mockNamespaceName,
	}

	// At this point, the cache is not started; we have to use the API reader
	// directly to bypass the cache.
	var namespace corev1.Namespace
	apiReader := mgr.GetAPIReader()
	if err := apiReader.Get(ctx, namespacedName, &namespace); apierrors.IsNotFound(err) {
		mgr.GetLogger().Info("Creating mock namespace", "namespace", mockNamespaceName)
		namespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: mockNamespaceName,
			},
		}
		if err := client.Create(ctx, &namespace); err != nil {
			mgr.GetLogger().Error(err, "Failed to create mock namespace", "namespace", mockNamespaceName)
			return err
		}
	} else if err != nil {
		mgr.GetLogger().Error(err, "Failed to get mock namespace", "namespace", mockNamespaceName)
		return err
	}
	return nil
}

// RegisterWithManager implements [base.Controller].
func (c *controller) RegisterWithManager(mgr ctrl.Manager) error {
	mgr.GetLogger().Info("Registering Mock Controller with Manager")
	ctx := context.Background()
	c.mgr = mgr

	if err := appv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := containersv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}
	if err := rddv1alpha1.AddToScheme(mgr.GetScheme()); err != nil {
		return err
	}

	if err := c.setupReconciler(ctx, mgr); err != nil {
		mgr.GetLogger().Error(err, "Failed to set up Mock ContainerReconciler")
		return err
	}
	if err := c.setupWebhookWithManager(mgr); err != nil {
		mgr.GetLogger().Error(err, "Failed to set up Mock Namespace webhook")
		return err
	}
	if err := c.createNamespace(ctx, mgr); err != nil {
		mgr.GetLogger().Error(err, "Failed to create Mock Namespace")
		return err
	}
	return nil
}
