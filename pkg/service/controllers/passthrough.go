// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"k8s.io/klog/v2"
)

// NewPassthroughHandler creates a new HTTP handler that proxies requests to the
// appropriate controller endpoint based on the discovery information.
//
// Note that this assumes the target endpoints do not contain path segments;
// that is, the endpoint URLs will be of the form "http://localhost:1234/" and
// not "http://localhost:1234/path".
func NewPassthroughHandler(discovery *ControllerManagerDiscovery) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log := klog.FromContext(r.Context())
		// The path is /<controller>/<endpoint>/...
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
		if len(parts) < 2 {
			log.V(5).Info("Invalid passthrough request path", "path", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		controller, endpoint := parts[0], parts[1]

		target, err := discovery.LookupPassthroughEndpoint(r.Context(), controller, endpoint)
		if err != nil {
			log.V(5).Info("Failed to lookup passthrough endpoint",
				"controller", controller, "endpoint", endpoint, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if target == "" {
			log.V(5).Info("Passthrough endpoint not found", "controller", controller, "endpoint", endpoint)
			http.NotFound(w, r)
			return
		}

		targetURL, err := url.Parse(target)
		if err != nil {
			log.V(5).Info("Failed to parse pass through target URL",
				"controller", controller, "endpoint", endpoint, "target", target, "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		log.V(5).Info("Proxying pass through request",
			"controller", controller, "endpoint", endpoint, "target", target)
		// NewSingleHostReverseProxy is cheap enough to make a new one per
		// request; we can consider caching later if performance is an issue.
		httputil.NewSingleHostReverseProxy(targetURL).ServeHTTP(w, r)
	})
}
