// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package options

import (
	apiextensionsapiserver "k8s.io/apiextensions-apiserver/pkg/apiserver"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/util/webhook"
	aggregatorapiserver "k8s.io/kube-aggregator/pkg/apiserver"
	aggregatorscheme "k8s.io/kube-aggregator/pkg/apiserver/scheme"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	"k8s.io/kubernetes/pkg/controlplane"
	controlplaneapiserver "k8s.io/kubernetes/pkg/controlplane/apiserver"
	generatedopenapi "k8s.io/kubernetes/pkg/generated/openapi"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// Config holds the configuration for the controlplane server.
type Config struct {
	Options CompletedOptions

	Aggregator    *aggregatorapiserver.Config
	ControlPlane  *controlplaneapiserver.Config
	APIExtensions *apiextensionsapiserver.Config

	ExtraConfig
}

// ExtraConfig holds the extra configuration for the controlplane server.
type ExtraConfig struct {
	// authentication
	AdminToken, UserToken string
	// Controllers holds the controller configuration for the controlplane server.
	Controllers controllers.CompletedOptions
}

type completedConfig struct {
	Options CompletedOptions

	Aggregator    aggregatorapiserver.CompletedConfig
	ControlPlane  controlplaneapiserver.CompletedConfig
	APIExtensions apiextensionsapiserver.CompletedConfig

	ExtraConfig
}

// CompletedConfig holds the completed configuration for the controlplane server.
type CompletedConfig struct {
	// Embed a private pointer that cannot be instantiated outside of this package.
	*completedConfig
}

// Complete fills in any fields not set that are required to have valid data.
func (c *Config) Complete() (CompletedConfig, error) {
	return CompletedConfig{&completedConfig{
		Options: c.Options,

		ControlPlane:  c.ControlPlane.Complete(),
		Aggregator:    c.Aggregator.Complete(),
		APIExtensions: c.APIExtensions.Complete(),

		ExtraConfig: c.ExtraConfig,
	}}, nil
}

// NewConfig creates all the self-contained pieces making up the controlplane server.
func NewConfig(opts CompletedOptions) (*Config, error) {
	c := &Config{
		Options: opts,
		ExtraConfig: ExtraConfig{
			Controllers: opts.Controllers,
		},
	}

	genericConfig, versionedInformers, storageFactory, err := controlplaneapiserver.BuildGenericConfig(
		opts.ControlPlane,
		[]*runtime.Scheme{legacyscheme.Scheme, apiextensionsapiserver.Scheme, aggregatorscheme.Scheme},
		controlplane.DefaultAPIResourceConfigSource(),
		generatedopenapi.GetOpenAPIDefinitions,
	)
	if err != nil {
		return nil, err
	}

	// set standalone config
	c.AdminToken, c.UserToken, err = opts.AdminAuthentication.ApplyTo(genericConfig)
	if err != nil {
		return nil, err
	}

	serviceResolver := webhook.NewDefaultServiceResolver()
	kubeAPIs, pluginInitializer, err := controlplaneapiserver.CreateConfig(opts.ControlPlane, genericConfig, versionedInformers, storageFactory, serviceResolver, nil)
	if err != nil {
		return nil, err
	}
	c.ControlPlane = kubeAPIs

	authInfoResolver := webhook.NewDefaultAuthenticationInfoResolverWrapper(kubeAPIs.ProxyTransport, kubeAPIs.Generic.EgressSelector, kubeAPIs.Generic.LoopbackClientConfig, kubeAPIs.Generic.TracerProvider)
	apiExtensions, err := controlplaneapiserver.CreateAPIExtensionsConfig(*kubeAPIs.Generic, kubeAPIs.VersionedInformers, pluginInitializer, opts.ControlPlane, 3, serviceResolver, authInfoResolver)
	if err != nil {
		return nil, err
	}
	c.APIExtensions = apiExtensions

	aggregator, err := controlplaneapiserver.CreateAggregatorConfig(*kubeAPIs.Generic, opts.ControlPlane, kubeAPIs.VersionedInformers, serviceResolver, kubeAPIs.ProxyTransport, kubeAPIs.Extra.PeerProxy, pluginInitializer)
	if err != nil {
		return nil, err
	}
	// IMPORTANT: disable the available condition controller in the aggregator
	// to prevent it to try to use Service and Endpoints resources which are not enabled in the controlplane.
	aggregator.ExtraConfig.DisableRemoteAvailableConditionController = true
	c.Aggregator = aggregator

	return c, nil
}
