// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	corev1scheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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
		Use:   "create NAME TEMPLATE",
		Short: "Create a new LimaVM resource",
		Long: `Create a new LimaVM resource with the specified template.

TEMPLATE can be:
- A ConfigMap name (if it's a valid Kubernetes DNS-1123 subdomain) in the namespace specified by -n
- A file path or URL (if it's not a valid ConfigMap name) - creates a ConfigMap with the VM name`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaCreateAction(cmd.Context(), args[0], args[1], namespace)
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
	if err := corev1scheme.AddToScheme(runtimeScheme); err != nil {
		return nil, fmt.Errorf("failed to add core types to scheme: %w", err)
	}
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

func limaCreateAction(ctx context.Context, name, template, namespace string) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}

	var templateConfigMapName string
	var createdConfigMap *corev1.ConfigMap // Track if we created a ConfigMap

	// Check if template is a valid ConfigMap name (DNS-1123 subdomain)
	validationErrs := validation.IsDNS1123Subdomain(template)
	if len(validationErrs) == 0 {
		templateConfigMapName = template
	} else {
		templateContent, err := os.ReadFile(template)
		if err != nil {
			return fmt.Errorf("failed to read template file %q: %w", template, err)
		}

		// Use the VM name as the ConfigMap name
		templateConfigMapName = name

		// Check if ConfigMap already exists
		existingConfigMap := &corev1.ConfigMap{}
		err = c.Get(ctx, types.NamespacedName{Name: templateConfigMapName, Namespace: namespace}, existingConfigMap)
		if err == nil {
			return fmt.Errorf("ConfigMap %q already exists in namespace %q, will not modify existing ConfigMap", templateConfigMapName, namespace)
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check for existing ConfigMap: %w", err)
		}

		// Create the ConfigMap with the template content
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      templateConfigMapName,
				Namespace: namespace,
			},
			Data: map[string]string{
				"template": string(templateContent),
			},
		}

		if err := c.Create(ctx, configMap); err != nil {
			return fmt.Errorf("failed to create ConfigMap %q: %w", templateConfigMapName, err)
		}
		logrus.Infof("ConfigMap %q created in namespace %q with template from file %q", templateConfigMapName, namespace, template)
		createdConfigMap = configMap // Track that we created this ConfigMap
	}

	// Create the LimaVM resource with the template reference
	running := false
	limaVM := &limav1alpha1.LimaVM{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: limav1alpha1.LimaVMSpec{
			TemplateRef: limav1alpha1.TemplateReference{
				Name: templateConfigMapName,
			},
			Running: &running,
		},
	}

	if err := c.Create(ctx, limaVM); err != nil {
		// If we created a ConfigMap and LimaVM creation failed, clean it up
		if createdConfigMap != nil {
			logrus.Warnf("LimaVM creation failed, cleaning up ConfigMap %q", createdConfigMap.Name)
			if deleteErr := c.Delete(ctx, createdConfigMap); deleteErr != nil {
				logrus.Errorf("Failed to clean up ConfigMap %q: %v", createdConfigMap.Name, deleteErr)
			}
		}
		return fmt.Errorf("failed to create LimaVM: %w", err)
	}
	logrus.Infof("LimaVM %q created in namespace %q with template ConfigMap %q", name, namespace, templateConfigMapName)

	// If we created a ConfigMap, set the LimaVM as its owner for auto-cleanup
	if createdConfigMap != nil {
		// Need to fetch the ConfigMap again to update it with owner reference
		configMapToUpdate := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{Name: createdConfigMap.Name, Namespace: createdConfigMap.Namespace}, configMapToUpdate); err != nil {
			logrus.Warnf("Failed to fetch ConfigMap for owner reference update: %v", err)
			return nil // Don't fail the whole operation
		}

		// Set owner reference using controller-runtime helper
		if err := ctrl.SetControllerReference(limaVM, configMapToUpdate, c.Scheme()); err != nil {
			logrus.Warnf("Failed to set owner reference on ConfigMap: %v", err)
			return nil // Don't fail the whole operation
		}

		// Update the ConfigMap with owner reference
		if err := c.Update(ctx, configMapToUpdate); err != nil {
			logrus.Warnf("Failed to update ConfigMap with owner reference: %v", err)
			return nil // Don't fail the whole operation
		}
		logrus.Debugf("Set LimaVM %q as owner of ConfigMap %q", name, createdConfigMap.Name)
	}

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
