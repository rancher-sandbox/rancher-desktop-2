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

// createReplaceKubeContext reads the instance kubeconfig from srcPath, renames
// its cluster/user/context entries to contextName, and merges them into
// ~/.kube/config.
func createReplaceKubeContext(contextName, srcPath string) error {
	src, err := clientcmd.LoadFromFile(srcPath)
	if err != nil {
		return fmt.Errorf("load instance kubeconfig: %w", err)
	}

	// Extract the single cluster and user (k3s names them "default").
	var cluster *clientcmdapi.Cluster
	for _, c := range src.Clusters {
		cluster = c
		break
	}
	if cluster == nil {
		return errors.New("instance kubeconfig has no cluster entry")
	}

	var user *clientcmdapi.AuthInfo
	for _, u := range src.AuthInfos {
		user = u
		break
	}
	if user == nil {
		return errors.New("instance kubeconfig has no user entry")
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
