// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
)

func newLimaCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "limavm",
		Short:   "Manage LimaVM resources",
		Long:    "Create, start, stop, and delete LimaVM virtual machines",
		Aliases: []string{"lima"},
	}
	command.AddCommand(
		newLimaCreateCommand(),
		newLimaStartCommand(),
		newLimaStopCommand(),
		newLimaDeleteCommand(),
	)
	return command
}

func newLimaCreateCommand() *cobra.Command {
	var namespace string
	command := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a new LimaVM resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaCreateAction(cmd.Context(), args[0], namespace)
		},
	}
	command.Flags().StringVarP(&namespace, "namespace", "n", "default", "Namespace for the LimaVM resource")
	return command
}

func newLimaStartCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "start NAME",
		Short: "Start a LimaVM by setting running=true",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaSetRunningAction(cmd.Context(), args[0], true)
		},
	}
	return command
}

func newLimaStopCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "stop NAME",
		Short: "Stop a LimaVM by setting running=false",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaSetRunningAction(cmd.Context(), args[0], false)
		},
	}
	return command
}

func newLimaDeleteCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a LimaVM resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaDeleteAction(cmd.Context(), args[0])
		},
	}
	return command
}

// getKubeClient returns a controller-runtime client configured for the RDD control plane.
func getKubeClient(ctx context.Context) (client.Client, error) {
	if err := ensureServiceRunning(ctx); err != nil {
		return nil, err
	}
	config, err := service.GetKubeRestConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}
	runtimeScheme := runtime.NewScheme()
	if err := limav1alpha1.AddToScheme(runtimeScheme); err != nil {
		return nil, fmt.Errorf("failed to add LimaVM types to scheme: %w", err)
	}
	c, err := client.New(config, client.Options{Scheme: runtimeScheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	return c, nil
}

// findLimaVM searches for a LimaVM with the given name across all namespaces.
func findLimaVM(ctx context.Context, c client.Client, name string) (*limav1alpha1.LimaVM, error) {
	vmList := &limav1alpha1.LimaVMList{}
	if err := c.List(ctx, vmList, client.MatchingFields{"metadata.name": name}); err != nil {
		return nil, fmt.Errorf("failed to list LimaVMs: %w", err)
	}
	if len(vmList.Items) == 0 {
		return nil, fmt.Errorf("LimaVM %q not found in any namespace", name)
	}
	return &vmList.Items[0], nil
}

func limaCreateAction(ctx context.Context, name, namespace string) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}

	running := false
	limaVM := &limav1alpha1.LimaVM{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: limav1alpha1.LimaVMSpec{
			Running: &running,
		},
	}

	if err := c.Create(ctx, limaVM); err != nil {
		return fmt.Errorf("failed to create LimaVM: %w", err)
	}
	logrus.Infof("LimaVM %q created in namespace %q", name, namespace)
	return nil
}

func limaSetRunningAction(ctx context.Context, name string, running bool) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}
	limaVM, err := findLimaVM(ctx, c, name)
	if err != nil {
		return err
	}

	// Create a patch to update the running field
	patch := client.MergeFrom(limaVM.DeepCopy())
	limaVM.Spec.Running = &running

	if err := c.Patch(ctx, limaVM, patch); err != nil {
		return fmt.Errorf("failed to update LimaVM: %w", err)
	}

	action := "stopped"
	if running {
		action = "started"
	}
	logrus.Infof("LimaVM %q %s in namespace %q", name, action, limaVM.Namespace)
	return nil
}

func limaDeleteAction(ctx context.Context, name string) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}

	limaVM, err := findLimaVM(ctx, c, name)
	if err != nil {
		return err
	}

	if err := c.Delete(ctx, limaVM); err != nil {
		return fmt.Errorf("failed to delete LimaVM: %w", err)
	}
	logrus.Infof("LimaVM %q deleted from namespace %q", name, limaVM.Namespace)
	return nil
}
