// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

package options

import (
	"context"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/spf13/pflag"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/group"
	"k8s.io/apiserver/pkg/authentication/request/bearertoken"
	authenticatorunion "k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authentication/user"
	genericapiserver "k8s.io/apiserver/pkg/server"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

const (
	// An rdd admin being member of system-admin.
	rddAdminUserName = "system-admin"
	// A non-admin user part of the "user" battery.
	rddUserUserName = "user"
)

// AdminAuthentication holds the configuration for the admin authentication in standalone mode.
type AdminAuthentication struct {
	// TODO: move into Secret in-cluster, maybe by using an "in-cluster" string as value
	ShardAdminTokenHashFilePath string
}

// NewAdminAuthentication returns a new AdminAuthentication for the given root directory
// where the token hash file should be written.
func NewAdminAuthentication() *AdminAuthentication {
	return &AdminAuthentication{
		ShardAdminTokenHashFilePath: filepath.Join(instance.TLSDir(), ".admin-token-store"),
	}
}

// Validate validates the admin authentication configuration.
func (s *AdminAuthentication) Validate() []error {
	if s == nil {
		return nil
	}
	// No validation needed for kubeconfig path since we use dynamic generation
	return nil
}

// AddFlags adds the flags for the admin authentication to the given FlagSet.
func (s *AdminAuthentication) AddFlags(fs *pflag.FlagSet) {
	if s == nil {
		return
	}

	fs.StringVar(&s.ShardAdminTokenHashFilePath, "authentication-admin-token-path", s.ShardAdminTokenHashFilePath,
		"Path to which the administrative token hash should be written at startup. If this is relative, it is relative to the service directory.")
}

// ApplyTo returns a new volatile admin token.
func (s *AdminAuthentication) ApplyTo(config *genericapiserver.Config) (volatileAdminToken, volatileUserToken string, err error) {
	volatileUserToken = uuid.New().String()
	volatileAdminToken = uuid.New().String()

	adminUser := &user.DefaultInfo{
		Name: rddAdminUserName,
		UID:  uuid.New().String(),
		Groups: []string{
			"system:masters",
		},
	}

	nonAdminUser := &user.DefaultInfo{
		Name:   rddUserUserName,
		UID:    uuid.New().String(),
		Groups: []string{},
	}

	newAuthenticator := group.NewAuthenticatedGroupAdder(bearertoken.New(authenticator.WrapAudienceAgnosticToken(config.Authentication.APIAudiences, authenticator.TokenFunc(func(_ context.Context, requestToken string) (*authenticator.Response, bool, error) {
		if requestToken == volatileAdminToken {
			return &authenticator.Response{User: adminUser}, true, nil
		}

		if requestToken == volatileUserToken {
			return &authenticator.Response{User: nonAdminUser}, true, nil
		}

		return nil, false, nil
	}))))

	config.Authentication.Authenticator = authenticatorunion.New(newAuthenticator, config.Authentication.Authenticator)

	return volatileAdminToken, volatileUserToken, nil
}

// CreateKubeConfig creates a kubeconfig with the given parameters (exported for dynamic generation).
func CreateKubeConfig(adminToken, userToken, baseHost, tlsServerName string, caData []byte) *clientcmdapi.Config {
	var kubeConfig clientcmdapi.Config
	kubeConfig.AuthInfos = map[string]*clientcmdapi.AuthInfo{
		rddAdminUserName: {Token: adminToken},
	}
	kubeConfig.Clusters = map[string]*clientcmdapi.Cluster{
		"root": {
			Server:                   baseHost,
			CertificateAuthorityData: caData,
			TLSServerName:            tlsServerName,
		},
	}
	kubeConfig.Contexts = map[string]*clientcmdapi.Context{
		"root": {Cluster: "root", AuthInfo: rddAdminUserName},
	}
	kubeConfig.CurrentContext = "root"

	if userToken != "" {
		kubeConfig.AuthInfos[rddUserUserName] = &clientcmdapi.AuthInfo{Token: userToken}
		kubeConfig.Contexts[rddUserUserName] = &clientcmdapi.Context{Cluster: "root", AuthInfo: rddUserUserName}
	}

	return &kubeConfig
}
