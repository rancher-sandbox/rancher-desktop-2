// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors
// SPDX-FileCopyrightText: The KCP Authors

// Package admission selects and configures the Kubernetes admission plugins for
// the embedded API server, including namespace lifecycle, service accounts,
// webhooks, owner-reference permission enforcement, and event rate limiting.
package admission

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/apiserver/pkg/admission/plugin/namespace/lifecycle"
	validatingadmissionpolicy "k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	mutatingwebhook "k8s.io/apiserver/pkg/admission/plugin/webhook/mutating"
	validatingwebhook "k8s.io/apiserver/pkg/admission/plugin/webhook/validating"
	"k8s.io/kubernetes/plugin/pkg/admission/deny"
	"k8s.io/kubernetes/plugin/pkg/admission/eventratelimit"
	"k8s.io/kubernetes/plugin/pkg/admission/gc"
	"k8s.io/kubernetes/plugin/pkg/admission/namespace/autoprovision"
	"k8s.io/kubernetes/plugin/pkg/admission/namespace/exists"
	"k8s.io/kubernetes/plugin/pkg/admission/serviceaccount"
)

// AllOrderedPlugins is the list of all the plugins in order.
var AllOrderedPlugins = []string{
	autoprovision.PluginName,  // NamespaceAutoProvision
	lifecycle.PluginName,      // NamespaceLifecycle
	exists.PluginName,         // NamespaceExists
	serviceaccount.PluginName, // ServiceAccount
	eventratelimit.PluginName, // EventRateLimit
	gc.PluginName,             // OwnerReferencesPermissionEnforcement

	// new admission plugins should generally be inserted above here
	// webhook, and deny plugins must go at the end
	mutatingwebhook.PluginName,           // MutatingAdmissionWebhook
	validatingadmissionpolicy.PluginName, // ValidatingAdmissionPolicy
	validatingwebhook.PluginName,         // ValidatingAdmissionWebhook
	deny.PluginName,                      // AlwaysDeny
}

// RegisterAllAdmissionPlugins registers all admission plugins.
func RegisterAllAdmissionPlugins(plugins *admission.Plugins) {
	autoprovision.Register(plugins)
	lifecycle.Register(plugins)
	exists.Register(plugins)
	serviceaccount.Register(plugins)
	eventratelimit.Register(plugins)
	gc.Register(plugins)

	mutatingwebhook.Register(plugins)
	validatingadmissionpolicy.Register(plugins)
	validatingwebhook.Register(plugins)
	deny.Register(plugins)
}

// DefaultOffAdmissionPlugins returns a set of admission plugins that should be disabled by default.
func DefaultOffAdmissionPlugins() sets.Set[string] {
	defaultOnPlugins := sets.New[string](
		lifecycle.PluginName,                 // NamespaceLifecycle
		serviceaccount.PluginName,            // ServiceAccount
		mutatingwebhook.PluginName,           // MutatingAdmissionWebhook
		validatingwebhook.PluginName,         // ValidatingAdmissionWebhook
		validatingadmissionpolicy.PluginName, // ValidatingAdmissionPolicy
	)

	return sets.New[string](AllOrderedPlugins...).Difference(defaultOnPlugins)
}
