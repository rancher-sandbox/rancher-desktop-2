// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"fmt"
	"strings"

	ctrlwebhookadmission "sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

// defaultK8sChannel is the channel used when Kubernetes is enabled without a version.
const defaultK8sChannel = "stable"

// AppDefaulter resolves channel aliases in App resources via a mutating
// admission webhook. It runs before the validating webhook, so an alias such
// as "stable" or "latest" becomes a concrete version that AppValidator accepts.
type AppDefaulter struct {
	channels map[string]string
}

// NewAppDefaulter parses k3sVersionsData once at construction time so that a
// malformed JSON fixture causes controller startup to fail rather than the
// first admission request.
func NewAppDefaulter(k3sVersionsData string) (*AppDefaulter, error) {
	channels, err := parseK3sChannels(k3sVersionsData)
	if err != nil {
		return nil, fmt.Errorf("failed to load Kubernetes version channels: %w", err)
	}
	return &AppDefaulter{channels: channels}, nil
}

var _ ctrlwebhookadmission.Defaulter[*v1alpha1.App] = &AppDefaulter{}

// Default resolves a channel alias in spec.kubernetes.version to a concrete
// version. When Kubernetes is enabled without a version, it uses the "stable"
// channel. It leaves a version that matches no channel unchanged for
// AppValidator to accept or reject.
func (d *AppDefaulter) Default(_ context.Context, app *v1alpha1.App) error {
	k8s := &app.Spec.Kubernetes
	version := k8s.Version
	if version == "" {
		if !k8s.Enabled {
			return nil
		}
		version = defaultK8sChannel
	}
	if resolved, ok := d.channels[strings.TrimPrefix(version, "v")]; ok {
		k8s.Version = resolved
	}
	return nil
}
