// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package kuberlr resolves a kubectl binary compatible with the cluster
// targeted by the user's kubeconfig. When the kubectl embedded in rdd falls
// within the supported version skew, rdd execs the embedded binary;
// otherwise rdd fetches a matching kubectl from dl.k8s.io into a shared
// cache and execs it in place. Modeled on github.com/flavio/kuberlr.
package kuberlr

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// envCacheDir lets tests (and operators) override the rdd-wide cache root.
// Anticipates a shared rdd cache that future consumers (k3s images, lima
// templates) can join — kuberlr appends its own subdirectory.
const envCacheDir = "RDD_CACHE_DIR"

// CacheDir returns the directory holding downloaded kubectl binaries. All
// rdd instances share this cache; kubectl does not vary per instance.
// Setting RDD_CACHE_DIR overrides the rdd-wide root; kuberlr always
// appends kubectl/<os>-<arch>/ inside that root.
//
//	macOS:   ~/Library/Caches/rancher-desktop/kubectl/<os>-<arch>/
//	Linux:   ~/.cache/rancher-desktop/kubectl/<os>-<arch>/  ($XDG_CACHE_HOME)
//	Windows: %LocalAppData%\rancher-desktop\kubectl\<os>-<arch>\
//
// TODO(eviction): the cache only grows — ~50 MB per minor version as a
// user roams across clusters — and a SIGKILL mid-download leaks the
// .kubectl-* temp file that defer os.Remove normally clears. Add a TTL or
// LRU sweep for both before the footprint grows noticeable.
func CacheDir() string {
	return filepath.Join(CacheRoot(), "kubectl", runtime.GOOS+"-"+runtime.GOARCH)
}

// CacheRoot returns the rdd-wide cache root. RDD_CACHE_DIR overrides the
// OS default. Future cache consumers (k3s images, Lima templates) should
// append their own subdirectory inside this root.
func CacheRoot() string {
	if root := os.Getenv(envCacheDir); root != "" {
		return root
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		panic(fmt.Errorf("could not determine user cache directory: %w", err))
	}
	return filepath.Join(cache, "rancher-desktop")
}
