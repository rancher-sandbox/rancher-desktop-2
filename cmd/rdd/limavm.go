// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

func newLimaVMCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "limavm",
		Short:   "Manage LimaVM resources",
		Long:    "Create, start, stop, and delete LimaVM virtual machines",
		Aliases: []string{"lima"},
	}
	command.AddCommand(
		newLimaVMCreateCommand(),
		newLimaVMStartCommand(),
		newLimaVMStopCommand(),
		newLimaVMDeleteCommand(),
		newLimaVMLogsCommand(),
	)
	return command
}

func newLimaVMCreateCommand() *cobra.Command {
	var namespace string
	var dryRun bool
	command := &cobra.Command{
		Use:   "create NAME TEMPLATE",
		Short: "Create a new LimaVM resource",
		Long: `Create a new LimaVM resource with the specified template.

TEMPLATE can be one of:
- A ConfigMap name (if it's a valid Kubernetes DNS-1123 subdomain) in the namespace specified by --namespace
- A file path (if it's not a valid ConfigMap name) - creates a ConfigMap with the VM name`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMCreateAction(cmd.Context(), args[0], args[1], namespace, dryRun)
		},
	}
	command.Flags().StringVarP(&namespace, "namespace", "n", metav1.NamespaceDefault, "Namespace for the LimaVM resource")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "If set, do not commit any changes to the cluster")
	return command
}

func newLimaVMStartCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "start NAME",
		Short: "Start a LimaVM by setting running=true",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMSetRunningAction(cmd.Context(), args[0], true)
		},
	}
	return command
}

func newLimaVMStopCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "stop NAME",
		Short: "Stop a LimaVM by setting running=false",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMSetRunningAction(cmd.Context(), args[0], false)
		},
	}
	return command
}

func newLimaVMDeleteCommand() *cobra.Command {
	var wait bool
	command := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a LimaVM resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMDeleteAction(cmd.Context(), args[0], wait)
		},
	}
	command.Flags().BoolVar(&wait, "wait", false, "Wait for the VM to be deleted before returning")
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

func createConfigMap(ctx context.Context, c client.Client, name, namespace, template string) (*corev1.ConfigMap, error) {
	content, err := os.ReadFile(template)
	if err != nil {
		return nil, fmt.Errorf("failed to read template file %q: %w", template, err)
	}

	// Check if ConfigMap already exists
	configMap := &corev1.ConfigMap{}
	err = c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, configMap)
	if err == nil {
		return nil, fmt.Errorf("ConfigMap %q already exists in namespace %q, will not modify existing ConfigMap", name, namespace)
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check for existing ConfigMap: %w", err)
	}

	// Create the ConfigMap with the template content
	configMap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			limav1alpha1.TemplateConfigMapKey: string(content),
		},
	}
	if err := c.Create(ctx, configMap); err != nil {
		return nil, fmt.Errorf("failed to create ConfigMap %q: %w", name, err)
	}
	logrus.Infof("ConfigMap %q created in namespace %q with template from file %q", name, namespace, template)
	return configMap, nil
}

func takeOwnership(ctx context.Context, c client.Client, limaVM *limav1alpha1.LimaVM, configMap *corev1.ConfigMap) error {
	if configMap != nil {
		// Need to fetch the ConfigMap again to update it with owner reference
		configMapToUpdate := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{Name: configMap.Name, Namespace: configMap.Namespace}, configMapToUpdate); err != nil {
			return fmt.Errorf("failed to fetch ConfigMap for owner reference update: %w", err)
		}
		// Set owner reference using controller-runtime helper
		if err := ctrl.SetControllerReference(limaVM, configMapToUpdate, c.Scheme()); err != nil {
			return fmt.Errorf("failed to set owner reference on ConfigMap: %w", err)
		}
		// Update the ConfigMap with owner reference
		if err := c.Update(ctx, configMapToUpdate); err != nil {
			return fmt.Errorf("failed to update ConfigMap with owner reference: %w", err)
		}
		logrus.Debugf("Set LimaVM %q as owner of ConfigMap %q", limaVM.ObjectMeta.Name, configMap.Name)
	}
	return nil
}

func limaVMCreateAction(ctx context.Context, name, template, namespace string, dryRun bool) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}

	var createdConfigMap *corev1.ConfigMap // Track if we created a ConfigMap

	// Check if template is a valid ConfigMap name (DNS-1123 subdomain)
	// https://kubernetes.io/docs/concepts/configuration/configmap/#configmap-object
	configMapName := template
	validationErrs := validation.IsDNS1123Subdomain(configMapName)
	if len(validationErrs) > 0 {
		// Use the VM name as the ConfigMap name
		configMapName = name
		if createdConfigMap, err = createConfigMap(ctx, c, configMapName, namespace, template); err != nil {
			return err
		}
	}

	// Delete createdConfigMap unless limaVM has been created and taken ownership of it.
	defer func() {
		if createdConfigMap != nil {
			logrus.Warnf("Cleaning up created ConfigMap %q", createdConfigMap.Name)
			_ = c.Delete(ctx, createdConfigMap)
		}
	}()

	// Create the LimaVM resource with the template reference
	running := false
	limaVM := &limav1alpha1.LimaVM{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: limav1alpha1.LimaVMSpec{
			TemplateRef: limav1alpha1.TemplateReference{
				Name: configMapName,
			},
			Running: running,
		},
	}

	var opts []client.CreateOption
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}

	// Create LimaVM resource.
	if err := c.Create(ctx, limaVM, opts...); err != nil {
		return fmt.Errorf("failed to create LimaVM: %w", err)
	}
	logrus.Infof("LimaVM %q created in namespace %q with template ConfigMap %q", name, namespace, configMapName)

	// If we created a ConfigMap, set the LimaVM as its owner for auto-cleanup
	if !dryRun {
		if err := takeOwnership(ctx, c, limaVM, createdConfigMap); err == nil {
			// Keep createdConfigMap until limaVM itself is deleted.
			createdConfigMap = nil
		}
	}
	return nil
}

func limaVMSetRunningAction(ctx context.Context, name string, running bool) error {
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
	limaVM.Spec.Running = running

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

func limaVMDeleteAction(ctx context.Context, name string, wait bool) error {
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

	if wait {
		// Wait for the LimaVM to be deleted
		for {
			var vm limav1alpha1.LimaVM
			time.Sleep(time.Second)
			err := c.Get(ctx, client.ObjectKeyFromObject(limaVM), &vm)
			if apierrors.IsNotFound(err) {
				break
			}
			if err == nil {
				if vm.UID != limaVM.UID {
					break
				}
				continue
			}
			return fmt.Errorf("failed to wait for LimaVM deletion: %w", err)
		}
	}

	logrus.Infof("LimaVM %q deleted from namespace %q", name, limaVM.Namespace)
	return nil
}

func newLimaVMLogsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "log INSTANCE",
		Aliases: []string{"logs"},
		Short:   "Show LimaVM logs",
		Long:    "Show hostagent logs for a LimaVM instance.",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logrus.SetLevel(logrus.InfoLevel)

			name := "ha.stderr.log"
			if ok, _ := cmd.Flags().GetBool("stdout"); ok {
				name = "ha.stdout.log"
			}
			logPath := filepath.Join(instance.LimaHome(), args[0], name)
			follow, _ := cmd.Flags().GetBool("follow")

			return tail.TailFile(cmd.Context(), cmd.OutOrStdout(), logPath, follow)
		},
	}
	command.Flags().BoolP("stdout", "o", false, "Print stdout instead of stderr")
	command.Flags().BoolP("follow", "f", false, "Follow log output")
	return command
}
