// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package testapiserver starts etcd and kube-apiserver (envtest) for tests.
// It runs them from a binary cache that test runs and test packages share.
package testapiserver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/gofrs/flock"
	"gotest.tools/v3/assert"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// binaryAssetsDirectory is the directory where setup-envtest keeps its
// binaries by default. That default comes from HOME (LocalAppData on
// Windows), and some tests set HOME to a temporary directory before they
// call Start. So the directory is resolved once at package init, before any
// test runs, and those tests still use the shared binaries instead of
// downloading them into their temporary HOME.
var binaryAssetsDirectory, binaryAssetsDirectoryErr = envtest.SetupEnvtestDefaultBinaryAssetsDirectory()

// Start runs envtest with the shared binary cache, stops it when the test
// ends, and returns the config for its API server. It waits while another
// test process starts envtest from the same cache.
func Start(t testing.TB) *rest.Config {
	t.Helper()
	assert.NilError(t, binaryAssetsDirectoryErr, "failed to find envtest binary cache directory")
	dir := binaryAssetsDirectory
	assert.NilError(t, os.MkdirAll(dir, 0o755), "failed to create envtest binary cache directory %s", dir)

	// envtest treats a binary that already exists as complete, so without
	// this lock a test package running in parallel could execute a binary
	// that another package is still writing.
	lock := flock.New(filepath.Join(dir, "envtest.lock"))
	assert.NilError(t, lock.Lock(), "failed to lock envtest binary cache directory %s", dir)
	// With BinaryAssetsDirectory unset, envtest downloads the binaries into a
	// new temporary directory on every start and never removes it.
	// On Windows, envtest looks for the binaries without their .exe suffix, so
	// it never finds the cached ones and downloads the archive again on every
	// start. When it unpacks the archive, it skips the files that already
	// exist, so the cache doesn't grow.
	env := &envtest.Environment{
		DownloadBinaryAssets:  true,
		BinaryAssetsDirectory: dir,
	}
	cfg, err := env.Start()
	unlockErr := lock.Unlock()
	assert.NilError(t, err, "failed to start environment; if an interrupted download left a broken binary, delete the directory under %s that holds it", dir)

	t.Cleanup(func() {
		err := env.Stop()
		// On Windows, `env.Stop()` returns an error because it can't stop
		// etcd gracefully. That doesn't affect the tests, so ignore every
		// error there. In CI, also ignore a timeout waiting for kube-apiserver
		// to stop.
		if runtime.GOOS != "windows" && err != nil {
			checkError := os.Getenv("CI") == ""
			checkError = checkError || !strings.Contains(err.Error(), "timeout waiting for process kube-apiserver to stop")
			if checkError {
				assert.Check(t, err, "failed to stop environment")
			}
		}
	})
	assert.NilError(t, unlockErr, "failed to unlock envtest binary cache directory %s", dir)
	return cfg
}
