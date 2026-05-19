// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// makeSrcKubeconfig writes a minimal instance kubeconfig (matching what
// storeKubeConfigToDisk produces) to dir/config.yaml and returns the path.
func makeSrcKubeconfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters["root"] = &clientcmdapi.Cluster{
		Server:                   "https://127.0.0.1:6443",
		CertificateAuthorityData: []byte("fake-ca"),
	}
	cfg.AuthInfos["system-admin"] = &clientcmdapi.AuthInfo{Token: "fake-token"}
	cfg.Contexts["root"] = &clientcmdapi.Context{Cluster: "root", AuthInfo: "system-admin"}
	cfg.CurrentContext = "root"

	path := filepath.Join(dir, "config.yaml")
	err := clientcmd.WriteToFile(*cfg, path)
	assert.NilError(t, err)
	return path
}

// loadDest loads the kubeconfig at destPath, failing the test on error.
func loadDest(t *testing.T, destPath string) *clientcmdapi.Config {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(destPath)
	assert.NilError(t, err)
	return cfg
}

func TestCreateReplaceKubeContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, ".kube", "config"))

	srcPath := makeSrcKubeconfig(t, dir)

	err := createReplaceKubeContext("rancher-desktop-2", srcPath)
	assert.NilError(t, err)

	destPath, _ := kubeConfigPath()
	cfg := loadDest(t, destPath)

	cluster, ok := cfg.Clusters["rancher-desktop-2"]
	assert.Assert(t, ok, "cluster rancher-desktop-2 not found")
	assert.Equal(t, cluster.Server, "https://127.0.0.1:6443")

	user, ok := cfg.AuthInfos["rancher-desktop-2"]
	assert.Assert(t, ok, "user rancher-desktop-2 not found")
	assert.Equal(t, user.Token, "fake-token")

	ctx, ok := cfg.Contexts["rancher-desktop-2"]
	assert.Assert(t, ok, "context rancher-desktop-2 not found")
	assert.Equal(t, ctx.Cluster, "rancher-desktop-2")
	assert.Equal(t, ctx.AuthInfo, "rancher-desktop-2")
}

func TestCreateReplaceKubeContext_MergesWithExisting(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	// Pre-populate with an unrelated context.
	existing := clientcmdapi.NewConfig()
	existing.Clusters["other"] = &clientcmdapi.Cluster{Server: "https://other:6443"}
	existing.AuthInfos["other-user"] = &clientcmdapi.AuthInfo{Token: "other-token"}
	existing.Contexts["other"] = &clientcmdapi.Context{Cluster: "other", AuthInfo: "other-user"}
	existing.CurrentContext = "other"
	assert.NilError(t, os.MkdirAll(filepath.Dir(destPath), 0o700))
	assert.NilError(t, clientcmd.WriteToFile(*existing, destPath))

	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))

	cfg := loadDest(t, destPath)

	// Existing entry is preserved.
	_, ok := cfg.Clusters["other"]
	assert.Assert(t, ok, "existing cluster should be preserved")

	// New entry was added.
	_, ok = cfg.Clusters["rancher-desktop-2"]
	assert.Assert(t, ok, "new cluster should be present")

	// current-context is not touched by createReplace.
	assert.Equal(t, cfg.CurrentContext, "other")
}

func TestDeleteKubeContext(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))

	assert.NilError(t, deleteKubeContext("rancher-desktop-2"))

	cfg := loadDest(t, destPath)
	_, ok := cfg.Clusters["rancher-desktop-2"]
	assert.Assert(t, !ok, "cluster should be deleted")
	_, ok = cfg.AuthInfos["rancher-desktop-2"]
	assert.Assert(t, !ok, "user should be deleted")
	_, ok = cfg.Contexts["rancher-desktop-2"]
	assert.Assert(t, !ok, "context should be deleted")
}

func TestDeleteKubeContext_NoOp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KUBECONFIG", filepath.Join(dir, ".kube", "config"))

	// Deleting a non-existent context on a missing file is a no-op.
	assert.NilError(t, deleteKubeContext("rancher-desktop-2"))
}

func TestSetCurrentKubeContext(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))

	assert.NilError(t, setCurrentKubeContext("rancher-desktop-2"))

	cfg := loadDest(t, destPath)
	assert.Equal(t, cfg.CurrentContext, "rancher-desktop-2")
}

func TestSetCurrentKubeContext_NoOpIfAlreadySet(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))
	assert.NilError(t, setCurrentKubeContext("rancher-desktop-2"))

	// Record mtime to verify the file is not rewritten.
	info1, err := os.Stat(destPath)
	assert.NilError(t, err)

	assert.NilError(t, setCurrentKubeContext("rancher-desktop-2"))

	info2, err := os.Stat(destPath)
	assert.NilError(t, err)
	assert.Equal(t, info1.ModTime(), info2.ModTime())
}

func TestClearCurrentKubeContext(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))
	assert.NilError(t, setCurrentKubeContext("rancher-desktop-2"))
	assert.NilError(t, clearCurrentKubeContext("rancher-desktop-2"))

	cfg := loadDest(t, destPath)
	assert.Equal(t, cfg.CurrentContext, "")
}

func TestClearCurrentKubeContext_NoOpIfDifferent(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	// Set current to a different context.
	existing := clientcmdapi.NewConfig()
	existing.CurrentContext = "other"
	assert.NilError(t, os.MkdirAll(filepath.Dir(destPath), 0o700))
	assert.NilError(t, clientcmd.WriteToFile(*existing, destPath))

	assert.NilError(t, clearCurrentKubeContext("rancher-desktop-2"))

	cfg := loadDest(t, destPath)
	assert.Equal(t, cfg.CurrentContext, "other")
}

func TestGetCurrentKubeContext(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, ".kube", "config")
	t.Setenv("KUBECONFIG", destPath)

	// Missing file returns empty string.
	current, err := getCurrentKubeContext()
	assert.NilError(t, err)
	assert.Equal(t, current, "")

	// After setting, returns the set value.
	srcPath := makeSrcKubeconfig(t, dir)
	assert.NilError(t, createReplaceKubeContext("rancher-desktop-2", srcPath))
	assert.NilError(t, setCurrentKubeContext("rancher-desktop-2"))

	current, err = getCurrentKubeContext()
	assert.NilError(t, err)
	assert.Equal(t, current, "rancher-desktop-2")
}
