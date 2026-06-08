// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// kubeConfigPath returns ~/.kube/config, or $KUBECONFIG if set.
// Computed dynamically so t.Setenv("HOME", …) works in tests.
func kubeConfigPath() (string, error) {
	if kc := os.Getenv("KUBECONFIG"); kc != "" {
		return kc, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// loadKubeConfig loads the kubeconfig at path; returns an empty config if absent.
func loadKubeConfig(path string) (*clientcmdapi.Config, error) {
	cfg, err := clientcmd.LoadFromFile(path)
	if os.IsNotExist(err) {
		return clientcmdapi.NewConfig(), nil
	}
	return cfg, err
}

// instanceClusterUser reads the k3s mirror kubeconfig at srcPath and returns its
// single cluster and user, which k3s names "default".
func instanceClusterUser(srcPath string) (*clientcmdapi.Cluster, *clientcmdapi.AuthInfo, error) {
	src, err := clientcmd.LoadFromFile(srcPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load instance kubeconfig: %w", err)
	}

	var cluster *clientcmdapi.Cluster
	for _, c := range src.Clusters {
		cluster = c
		break
	}
	if cluster == nil {
		return nil, nil, errors.New("instance kubeconfig has no cluster entry")
	}

	var user *clientcmdapi.AuthInfo
	for _, u := range src.AuthInfos {
		user = u
		break
	}
	if user == nil {
		return nil, nil, errors.New("instance kubeconfig has no user entry")
	}
	return cluster, user, nil
}

// createReplaceKubeContext reads the instance kubeconfig from srcPath, renames
// its cluster/user/context entries to contextName, and merges them into
// ~/.kube/config.
func createReplaceKubeContext(contextName, srcPath string) error {
	cluster, user, err := instanceClusterUser(srcPath)
	if err != nil {
		return err
	}

	destPath, err := kubeConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("create .kube dir: %w", err)
	}

	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		return fmt.Errorf("load ~/.kube/config: %w", err)
	}

	cfg.Clusters[contextName] = cluster
	cfg.AuthInfos[contextName] = user
	cfg.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  contextName,
		AuthInfo: contextName,
	}

	return clientcmd.WriteToFile(*cfg, destPath)
}

// deleteKubeContext removes the cluster, user, and context named contextName
// from ~/.kube/config. No-op if any entry is absent.
func deleteKubeContext(contextName string) error {
	destPath, err := kubeConfigPath()
	if err != nil {
		return err
	}

	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		return fmt.Errorf("load ~/.kube/config: %w", err)
	}

	delete(cfg.Clusters, contextName)
	delete(cfg.AuthInfos, contextName)
	delete(cfg.Contexts, contextName)

	return clientcmd.WriteToFile(*cfg, destPath)
}

// writeInstanceKubeConfig writes a standalone kubeconfig to destPath holding
// only contextName, set as the current context. rdd run points KUBECONFIG at
// this file, so the instance credentials live in the instance directory rather
// than a per-invocation temp file.
func writeInstanceKubeConfig(contextName, srcPath, destPath string) error {
	cluster, user, err := instanceClusterUser(srcPath)
	if err != nil {
		return err
	}

	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[contextName] = cluster
	cfg.AuthInfos[contextName] = user
	cfg.Contexts[contextName] = &clientcmdapi.Context{
		Cluster:  contextName,
		AuthInfo: contextName,
	}
	cfg.CurrentContext = contextName

	if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
		return fmt.Errorf("create instance dir: %w", err)
	}
	return clientcmd.WriteToFile(*cfg, destPath)
}

// removeInstanceKubeConfig deletes the standalone instance kubeconfig at
// destPath. No-op if the file is already absent.
func removeInstanceKubeConfig(destPath string) error {
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove instance kubeconfig: %w", err)
	}
	return nil
}

// getCurrentKubeContext returns current-context from ~/.kube/config,
// or empty string if unset or the file is absent.
func getCurrentKubeContext() (string, error) {
	destPath, err := kubeConfigPath()
	if err != nil {
		return "", err
	}
	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		return "", fmt.Errorf("load ~/.kube/config: %w", err)
	}
	return cfg.CurrentContext, nil
}

// setCurrentKubeContext sets current-context in ~/.kube/config. No-op if already set.
func setCurrentKubeContext(name string) error {
	destPath, err := kubeConfigPath()
	if err != nil {
		return err
	}
	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		return fmt.Errorf("load ~/.kube/config: %w", err)
	}
	if cfg.CurrentContext == name {
		return nil
	}
	cfg.CurrentContext = name
	return clientcmd.WriteToFile(*cfg, destPath)
}

// clearCurrentKubeContext clears current-context if it matches name; no-op otherwise.
func clearCurrentKubeContext(name string) error {
	destPath, err := kubeConfigPath()
	if err != nil {
		return err
	}
	cfg, err := loadKubeConfig(destPath)
	if err != nil {
		return fmt.Errorf("load ~/.kube/config: %w", err)
	}
	if cfg.CurrentContext != name {
		return nil
	}
	cfg.CurrentContext = ""
	return clientcmd.WriteToFile(*cfg, destPath)
}
