// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package options

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/k3s-io/kine/pkg/endpoint"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/admission"
	genericfeatures "k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/util/keyutil"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/klog/v2"
	controlplaneapiserveroptions "k8s.io/kubernetes/pkg/controlplane/apiserver/options"
	"k8s.io/kubernetes/pkg/features"
	kubeoptions "k8s.io/kubernetes/pkg/kubeapiserver/options"
	"k8s.io/kubernetes/pkg/serviceaccount"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	rddadmission "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/admission"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/datastore"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/tokengetter"
)

// Options holds the configuration for the controlplane server.
type Options struct {
	ControlPlane        controlplaneapiserveroptions.Options
	Datastore           datastore.Options
	AdminAuthentication AdminAuthentication
	Controllers         controllers.Options

	Extra ExtraOptions
}

// ExtraOptions holds the extra configuration for the controlplane server.
type ExtraOptions struct {
	RootDir string
}

type completedOptions struct {
	ControlPlane        controlplaneapiserveroptions.CompletedOptions
	Datastore           datastore.CompletedOptions
	AdminAuthentication AdminAuthentication
	Controllers         controllers.CompletedOptions

	Extra ExtraOptions
}

// CompletedOptions holds the completed configuration for the controlplane server.
type CompletedOptions struct {
	*completedOptions
}

// NewOptions creates a new Options with default parameters.
func NewOptions(ctx context.Context, rootDir string) *Options {
	o := &Options{
		ControlPlane:        *controlplaneapiserveroptions.NewOptions(),
		Datastore:           *datastore.NewOptions(),
		AdminAuthentication: *NewAdminAuthentication(),
		Controllers:         *controllers.NewOptions(),
		Extra: ExtraOptions{
			RootDir: rootDir,
		},
	}

	// Disable node related features to prevent the need for informers.
	_ = utilfeature.DefaultMutableFeatureGate.OverrideDefault(features.ServiceAccountTokenNodeBindingValidation, false)
	_ = utilfeature.DefaultMutableFeatureGate.OverrideDefault(features.ServiceAccountTokenNodeBinding, false)

	// Disable apiserver identity leases because this is a single-instance control plane.
	_ = utilfeature.DefaultMutableFeatureGate.OverrideDefault(genericfeatures.APIServerIdentity, false)
	// UnknownVersionInteroperabilityProxy proxies between mixed-version apiservers and
	// depends on APIServerIdentity; neither applies to a single-instance control plane.
	_ = utilfeature.DefaultMutableFeatureGate.OverrideDefault(genericfeatures.UnknownVersionInteroperabilityProxy, false)

	factory := func(factory informers.SharedInformerFactory) serviceaccount.ServiceAccountTokenGetter {
		return tokengetter.NewGetterFromClient(factory.Core().V1().Secrets().Lister(), factory.Core().V1().ServiceAccounts().Lister())
	}

	// We use KCP form of the authentication options as it does not contain nodes and pods informers.
	o.ControlPlane.Authentication = kubeoptions.NewBuiltInAuthenticationOptions().
		WithAnonymous().
		WithBootstrapToken().
		WithClientCert().
		WithOIDC().
		WithRequestHeader().
		WithServiceAccounts().
		WithTokenFile().
		WithWebHook()

	o.ControlPlane.Authentication.ServiceAccounts.OptionalTokenGetter = factory

	o.ControlPlane.Authentication.ServiceAccounts.Issuers = []string{"https://rdd.default.svc"}
	kineListener := endpoint.KineSocket
	if runtime.GOOS == "windows" {
		// On Windows, unix:// isn't supported; however, kine/grpc doesn't support
		// named pipes either.  Try to find a free port and use that.
		if l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0"); err != nil {
			klog.Background().Error(err, "failed to find port for kine")
		} else {
			kineListener = fmt.Sprintf("http://%s", l.Addr())
			_ = l.Close()
		}
	}
	o.ControlPlane.Etcd.StorageConfig.Transport.ServerList = []string{kineListener}
	o.ControlPlane.Features.EnablePriorityAndFairness = false
	// turn on the watch cache
	o.ControlPlane.Etcd.EnableWatchCache = true

	// Flush out the default admission plugins and set the ones we want below.
	o.ControlPlane.Admission.GenericAdmission.Plugins = admission.NewPlugins()

	return o
}

// AddFlags adds flags for a specific APIServer to the specified FlagSet.
func (o *Options) AddFlags(flags *cliflag.NamedFlagSets) {
	o.ControlPlane.AddFlags(flags)

	etcdServers := flags.FlagSet("etcd").Lookup("etcd-servers")
	etcdServers.Usage += " By default an embedded etcd server is started."

	o.AdminAuthentication.AddFlags(flags.FlagSet("RDD Standalone Authentication"))
	o.Controllers.AddFlags(flags.FlagSet("Options"))
}

// Complete fills in any fields not set that are required to have valid data.
func (o *Options) Complete(ctx context.Context) (*CompletedOptions, error) {
	servers := o.ControlPlane.Etcd.StorageConfig.Transport.ServerList
	if len(servers) > 0 && strings.HasPrefix(servers[0], "http") {
		// use default etcd port instead of unix://kine.socket
		// this works with e.g. `--etcd-servers http://127.0.0.1:2379`
		hostPort := "127.0.0.1:2379"
		if u, err := url.Parse(servers[0]); err == nil {
			hostPort = u.Host
			if _, _, err := net.SplitHostPort(hostPort); err != nil {
				// The hostPort part does not contain a port; add it.
				hostPort = net.JoinHostPort(u.Hostname(), "2379")
			}
		}
		o.Datastore.EndpointConfig.Listener = fmt.Sprintf("tcp://%s", hostPort)
	}
	klog.Background().Info("enabling embedded kine/sqlite server", "listener", o.Datastore.EndpointConfig.Listener)
	o.Datastore.Enabled = true

	completedControllers := o.Controllers.Complete()

	var serviceAccountFile string
	if len(o.ControlPlane.Authentication.ServiceAccounts.KeyFiles) == 0 {
		// use sa.key in TLS directory and auto-generate if not existing
		serviceAccountFile = filepath.Join(instance.TLSDir(), "sa.key")
		if _, err := os.Stat(serviceAccountFile); os.IsNotExist(err) {
			klog.Background().WithValues("file", serviceAccountFile).Info("generating service account key file")
			key, err := rsa.GenerateKey(cryptorand.Reader, 4096)
			if err != nil {
				return nil, fmt.Errorf("error generating service account private key: %w", err)
			}

			encoded, err := keyutil.MarshalPrivateKeyToPEM(key)
			if err != nil {
				return nil, fmt.Errorf("error converting service account private key to PEM format: %w", err)
			}
			if err := keyutil.WriteKey(serviceAccountFile, encoded); err != nil {
				return nil, fmt.Errorf("error writing service account private key file %q: %w", serviceAccountFile, err)
			}
		} else if err != nil {
			return nil, fmt.Errorf("error checking service account key file %q: %w", serviceAccountFile, err)
		}

		// set the key file to controlplane server
		o.ControlPlane.Authentication.ServiceAccounts.KeyFiles = []string{serviceAccountFile}

		if o.ControlPlane.ServiceAccountSigningKeyFile == "" {
			o.ControlPlane.ServiceAccountSigningKeyFile = serviceAccountFile
		}
	}

	// override set of admission plugins - always enable essential ones
	rddadmission.RegisterAllAdmissionPlugins(o.ControlPlane.Admission.GenericAdmission.Plugins)
	o.ControlPlane.Admission.GenericAdmission.DisablePlugins = sets.List[string](rddadmission.DefaultOffAdmissionPlugins())
	o.ControlPlane.Admission.GenericAdmission.RecommendedPluginOrder = rddadmission.AllOrderedPlugins

	// Force external address to localhost to ensure certificate includes 127.0.0.1 in SAN list
	o.ControlPlane.SecureServing.ExternalAddress = net.IPv4(127, 0, 0, 1)
	o.ControlPlane.SecureServing.BindAddress = net.IPv4(127, 0, 0, 1)

	// Fall back to a random port if the configured port is busy.
	if o.ControlPlane.SecureServing.BindPort != 0 {
		actualPort, err := controllers.ResolvePort(ctx, o.ControlPlane.SecureServing.BindPort)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve secure port: %w", err)
		}
		o.ControlPlane.SecureServing.BindPort = actualPort
	}

	o.ControlPlane.SecureServing.ServerCert.CertDirectory = instance.TLSDir()

	var err error
	if !filepath.IsAbs(o.ControlPlane.SecureServing.ServerCert.CertDirectory) {
		o.ControlPlane.SecureServing.ServerCert.CertDirectory, err = filepath.Abs(o.ControlPlane.SecureServing.ServerCert.CertDirectory)
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(o.AdminAuthentication.ShardAdminTokenHashFilePath) {
		o.AdminAuthentication.ShardAdminTokenHashFilePath, err = filepath.Abs(o.AdminAuthentication.ShardAdminTokenHashFilePath)
		if err != nil {
			return nil, err
		}
	}

	completedServerRunOptions, err := o.ControlPlane.Complete(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	completedDatastore := o.Datastore.Complete()

	return &CompletedOptions{
		completedOptions: &completedOptions{
			ControlPlane:        completedServerRunOptions,
			Datastore:           completedDatastore,
			AdminAuthentication: o.AdminAuthentication,
			Controllers:         completedControllers,
			Extra:               o.Extra,
		},
	}, nil
}

// Validate validates the controlplane server options.
func (o *CompletedOptions) Validate() []error {
	var errs []error

	errs = append(errs, o.ControlPlane.Validate()...)
	errs = append(errs, o.Datastore.Validate()...)
	errs = append(errs, o.AdminAuthentication.Validate()...)
	errs = append(errs, o.Controllers.Validate()...)

	return errs
}
