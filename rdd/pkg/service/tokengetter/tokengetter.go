// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

// Package tokengetter implements ServiceAccountTokenGetter for the embedded API
// server's token authenticator. It reads ServiceAccount and Secret objects
// directly from the lister cache, avoiding a circular dependency on the API
// server during authentication.
package tokengetter

import (
	"context"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/kubernetes/pkg/serviceaccount"
)

// clientGetter implements ServiceAccountTokenGetter using a factory function.
type clientGetter struct {
	secretLister         v1listers.SecretLister
	serviceAccountLister v1listers.ServiceAccountLister
}

// NewGetterFromClient returns a ServiceAccountTokenGetter that
// uses the specified client to retrieve service accounts, secrets and
// return errors for nodes and pods.
func NewGetterFromClient(secretLister v1listers.SecretLister, serviceAccountLister v1listers.ServiceAccountLister) serviceaccount.ServiceAccountTokenGetter {
	return clientGetter{secretLister, serviceAccountLister}
}

func (c clientGetter) GetServiceAccount(_ context.Context, namespace, name string) (*v1.ServiceAccount, error) {
	return c.serviceAccountLister.ServiceAccounts(namespace).Get(name)
}

func (c clientGetter) GetPod(_ context.Context, _, name string) (*v1.Pod, error) {
	return nil, apierrors.NewNotFound(v1.Resource("pods"), name)
}

func (c clientGetter) GetSecret(_ context.Context, namespace, name string) (*v1.Secret, error) {
	return c.secretLister.Secrets(namespace).Get(name)
}

func (c clientGetter) GetNode(_ context.Context, name string) (*v1.Node, error) {
	return nil, apierrors.NewNotFound(v1.Resource("nodes"), name)
}
