// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package kuberlr resolves a kubectl binary for the cluster targeted
// by the user's kubeconfig: it execs the embedded kubectl when
// acceptable, otherwise a cached or freshly downloaded one from
// dl.k8s.io. Modeled on github.com/flavio/kuberlr, but where kuberlr
// accepts any kubectl within the ±1 minor skew, rdd insists on one
// that also supports every server feature (see acceptable).
package kuberlr

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/blang/semver/v4"
)

// envCacheDir lets tests and operators override the rdd-wide cache
// root, anticipating future consumers (k3s images, lima templates).
const envCacheDir = "RDD_CACHE_DIR"

// CacheDir returns the directory holding downloaded kubectl binaries,
// shared across rdd instances; RDD_CACHE_DIR overrides the root.
//
//	macOS:   ~/Library/Caches/rancher-desktop/kubectl/<os>-<arch>/
//	Linux:   ~/.cache/rancher-desktop/kubectl/<os>-<arch>/  ($XDG_CACHE_HOME)
//	Windows: %LocalAppData%\rancher-desktop\kubectl\<os>-<arch>\
//
// TODO(eviction): the cache only grows (~50 MB per minor version), and
// a SIGKILL mid-download can leak the .kubectl-* temp file. Add a TTL
// or LRU sweep before the footprint grows noticeable.
func CacheDir() string {
	return filepath.Join(CacheRoot(), "kubectl", runtime.GOOS+"-"+runtime.GOARCH)
}

// CacheRoot returns the rdd-wide cache root (RDD_CACHE_DIR overrides
// the OS default); future cache consumers should nest under it.
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

// bestCached returns the path of the highest-versioned cached kubectl
// acceptable for server; ok is false when none qualifies. Any
// acceptable patch level counts as a hit, so a cache warmed with an
// older patch release keeps working without network access.
func bestCached(server semver.Version) (path string, ok bool) {
	entries, err := os.ReadDir(CacheDir())
	if err != nil {
		return "", false
	}
	var best semver.Version
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		v, valid := parseCachedName(entry.Name())
		if !valid {
			continue
		}
		if acceptable(v, server) && (!ok || v.GT(best)) {
			best, ok = v, true
		}
	}
	if !ok {
		return "", false
	}
	return cachedPath(best), true
}

// parseCachedName extracts the version from a cache file name written
// by cachedPath; temp files and foreign names report valid=false.
func parseCachedName(name string) (v semver.Version, valid bool) {
	if runtime.GOOS == "windows" {
		var found bool
		if name, found = strings.CutSuffix(name, ".exe"); !found {
			return semver.Version{}, false
		}
	}
	rest, found := strings.CutPrefix(name, "kubectl-v")
	if !found {
		return semver.Version{}, false
	}
	v, err := semver.Parse(rest)
	return v, err == nil
}
