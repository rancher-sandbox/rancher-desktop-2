// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	corev1scheme "k8s.io/client-go/kubernetes/scheme"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/kubectl/pkg/cmd/util/editor"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	limav1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/lima/v1alpha1"
	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/tail"
)

// limaLongWaitTimeout is the default --timeout for VM boot and shutdown waits,
// which routinely take minutes.
const limaLongWaitTimeout = 5 * time.Minute

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
		newLimaVMRestartCommand(),
		newLimaVMDeleteCommand(),
		newLimaVMLogsCommand(),
		newLimaVMShellCommand(),
		newLimaVMEditCommand(),
	)
	return command
}

func newLimaVMEditCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "edit NAME",
		Short: "Edit the Lima template ConfigMap for a VM",
		Long:  "Open the LimaVM resource manifest in the default editor for editing.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMEditAction(cmd.Context(), args[0])
		},
	}
	return command
}

func limaVMEditAction(ctx context.Context, name string) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}
	limaVM, err := findLimaVM(ctx, c, name)
	if err != nil {
		return err
	}

	configMapName := limaVM.Status.TemplateConfigMap
	if configMapName == "" {
		return fmt.Errorf("LimaVM %q does not have a template ConfigMap", name)
	}

	configMapKey := types.NamespacedName{Name: configMapName, Namespace: limaVM.Namespace}

	configMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, configMapKey, configMap); err != nil {
		return fmt.Errorf("failed to get template ConfigMap %q: %w", configMapName, err)
	}

	templateData, exists := configMap.Data[limav1alpha1.TemplateConfigMapKey]
	if !exists {
		return fmt.Errorf("template ConfigMap %q does not contain key %q", configMapName, limav1alpha1.TemplateConfigMapKey)
	}

	edit := editor.NewDefaultEditor(editorEnvs())
	// currentContent is what we show in the editor. On the first iteration it
	// is the original template; on retries it is the user's previous edits.
	currentContent := templateData

	for {
		updatedBytes, path, err := edit.LaunchTempFile(name+"-template-", ".yaml", strings.NewReader(currentContent))
		if err != nil {
			return fmt.Errorf("failed to launch editor: %w", err)
		}
		os.Remove(path)

		updatedContent := strings.TrimSpace(string(updatedBytes))
		if updatedContent == "" {
			return errors.New("template data was cleared, aborting edit")
		}

		if updatedContent == templateData {
			logrus.Info("No changes made to template, skipping update")
			return nil
		}

		// Re-fetch the ConfigMap to get the latest resourceVersion before each
		// update attempt, so a concurrent modification doesn't cause a stale-
		// object conflict on retry.
		if err := c.Get(ctx, configMapKey, configMap); err != nil {
			return fmt.Errorf("failed to get template ConfigMap %q: %w", configMapName, err)
		}
		configMap.Data[limav1alpha1.TemplateConfigMapKey] = updatedContent

		if err := c.Update(ctx, configMap); err != nil {
			fmt.Fprintf(os.Stderr, "error: failed to update template ConfigMap: %v\n", err)
			if !confirmRetry(os.Stdin, os.Stderr) {
				return errors.New("aborted")
			}
			// Preserve the user's edits for the next iteration.
			currentContent = updatedContent
			continue
		}

		logrus.Infof("Template ConfigMap %q updated successfully", configMapName)
		return nil
	}
}

// confirmRetry asks the user on r/w whether they want to reopen the editor
// with their previous edits. Returns true if the user answers "y" or "yes".
func confirmRetry(r io.Reader, w io.Writer) bool {
	fmt.Fprintf(w, "Retry editing with your previous changes? [y/N] ")
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return ans == "y" || ans == "yes"
}

func newLimaVMCreateCommand() *cobra.Command {
	var namespace string
	var dryRun bool
	var start bool
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "create NAME TEMPLATE",
		Short: "Create a new LimaVM resource",
		Long: `Create a new LimaVM resource with the specified template.

TEMPLATE can be one of:
- A ConfigMap name (if it's a valid Kubernetes DNS-1123 subdomain) in the namespace specified by --namespace
- A file path (if it's not a valid ConfigMap name) - creates a ConfigMap with the VM name`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMCreateAction(cmd.Context(), args[0], args[1], namespace, dryRun, start, wait, timeout)
		},
	}
	command.Flags().StringVarP(&namespace, "namespace", "n", metav1.NamespaceDefault, "Namespace for the LimaVM resource")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "If set, do not commit any changes to the cluster")
	command.Flags().BoolVar(&start, "start", false, "Start the VM after creation")
	command.Flags().BoolVar(&wait, "wait", true, "Wait for the operation to complete (only applies with --start)")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout, "Timeout for --wait; ignored without --start or with --wait=false (0 means wait indefinitely)")
	return command
}

func newLimaVMStartCommand() *cobra.Command {
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "start NAME",
		Short: "Start a LimaVM by setting running=true",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMSetRunningAction(cmd.Context(), args[0], true, wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true, "Wait for the operation to complete")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")
	return command
}

func newLimaVMStopCommand() *cobra.Command {
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "stop NAME",
		Short: "Stop a LimaVM by setting running=false",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMSetRunningAction(cmd.Context(), args[0], false, wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true, "Wait for the operation to complete")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")
	return command
}

func newLimaVMRestartCommand() *cobra.Command {
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "restart NAME",
		Short: "Restart a LimaVM instance",
		Long:  "Set the restartRequested annotation and spec.running=true to restart the instance.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMRestartAction(cmd.Context(), args[0], wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true, "Wait for the operation to complete")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")
	return command
}

func limaVMRestartAction(ctx context.Context, name string, wait bool, timeout time.Duration) error {
	c, err := getKubeClient(ctx)
	if err != nil {
		return err
	}
	limaVM, err := findLimaVM(ctx, c, name)
	if err != nil {
		return err
	}

	beforeCount := limaVM.Status.RestartCount

	patch := client.MergeFrom(limaVM.DeepCopy())
	limaVM.Spec.Running = true
	if limaVM.Annotations == nil {
		limaVM.Annotations = make(map[string]string)
	}
	limaVM.Annotations[limav1alpha1.AnnotationRestartRequested] = time.Now().UTC().Format(time.RFC3339)

	if err := c.Patch(ctx, limaVM, patch); err != nil {
		return fmt.Errorf("failed to restart LimaVM: %w", err)
	}

	logrus.Infof("LimaVM %q restart requested in namespace %q", name, limaVM.Namespace)

	if wait {
		waitCtx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
		defer cancel()
		key := client.ObjectKeyFromObject(limaVM)
		if err := watchUntil(waitCtx, c, key, func(vm *limav1alpha1.LimaVM) bool {
			return vm.Status.RestartCount > beforeCount
		}); err != nil {
			return cliexit.Classify(fmt.Errorf("failed waiting for restart to complete: %w", err))
		}
		logrus.Infof("LimaVM %q restarted in namespace %q", name, limaVM.Namespace)
	}
	return nil
}

func newLimaVMDeleteCommand() *cobra.Command {
	var wait bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a LimaVM resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return limaVMDeleteAction(cmd.Context(), args[0], wait, timeout)
		},
	}
	command.Flags().BoolVar(&wait, "wait", true, "Wait for the VM to be deleted before returning")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout, "Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")
	return command
}

// getKubeClient returns a controller-runtime client configured for the RDD control plane.
// The returned client supports Watch for event-driven waiting on resource changes.
func getKubeClient(ctx context.Context) (client.WithWatch, error) {
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
	c, err := client.NewWithWatch(config, client.Options{Scheme: runtimeScheme})
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

func limaVMCreateAction(ctx context.Context, name, template, namespace string, dryRun, start, wait bool, timeout time.Duration) error {
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
	limaVM := &limav1alpha1.LimaVM{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: limav1alpha1.LimaVMSpec{
			TemplateRef: limav1alpha1.TemplateReference{
				Name: configMapName,
			},
			Running: start,
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

	if start && wait && !dryRun {
		waitCtx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
		defer cancel()
		key := client.ObjectKeyFromObject(limaVM)
		if err := watchUntil(waitCtx, c, key, func(vm *limav1alpha1.LimaVM) bool {
			return apimeta.IsStatusConditionPresentAndEqual(vm.Status.Conditions, "Running", metav1.ConditionTrue)
		}); err != nil {
			return cliexit.Classify(fmt.Errorf("failed waiting for LimaVM to start: %w", err))
		}
		logrus.Infof("LimaVM %q started in namespace %q", name, namespace)
	}
	return nil
}

func limaVMSetRunningAction(ctx context.Context, name string, running, wait bool, timeout time.Duration) error {
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
	desiredStatus := metav1.ConditionFalse
	if running {
		action = "started"
		desiredStatus = metav1.ConditionTrue
	}
	logrus.Infof("LimaVM %q %s in namespace %q", name, action, limaVM.Namespace)

	if wait {
		waitCtx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
		defer cancel()
		key := client.ObjectKeyFromObject(limaVM)
		if err := watchUntil(waitCtx, c, key, func(vm *limav1alpha1.LimaVM) bool {
			return apimeta.IsStatusConditionPresentAndEqual(vm.Status.Conditions, "Running", desiredStatus)
		}); err != nil {
			return cliexit.Classify(fmt.Errorf("failed waiting for LimaVM to be %s: %w", action, err))
		}
	}
	return nil
}

// watchUntil watches a LimaVM until the check function returns true.
// It reads the current state first, then watches from that resource version
// to avoid missing events between reads.
func watchUntil(ctx context.Context, c client.WithWatch, key client.ObjectKey, check func(*limav1alpha1.LimaVM) bool) error {
	var vm limav1alpha1.LimaVM
	if err := c.Get(ctx, key, &vm); err != nil {
		return err
	}
	if check(&vm) {
		return nil
	}

	vmList := &limav1alpha1.LimaVMList{}
	watcher, err := c.Watch(ctx, vmList,
		client.InNamespace(key.Namespace),
		client.MatchingFields{"metadata.name": key.Name},
		&client.ListOptions{Raw: &metav1.ListOptions{ResourceVersion: vm.ResourceVersion}},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for ev := range watcher.ResultChan() {
		switch ev.Type {
		case k8swatch.Error:
			return apierrors.FromObject(ev.Object)
		case k8swatch.Deleted:
			return fmt.Errorf("LimaVM %q was deleted while waiting", key.Name)
		}
		updated, ok := ev.Object.(*limav1alpha1.LimaVM)
		if ok && check(updated) {
			return nil
		}
	}
	// ResultChan closed. If ctx ended, return its error. Otherwise the
	// watch dropped (server close, proxy timeout, watch expiry); re-read
	// and re-evaluate to distinguish a satisfied predicate from a drop.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Get(ctx, key, &vm); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("LimaVM %q was deleted while waiting", key.Name)
		}
		return err
	}
	if check(&vm) {
		return nil
	}
	return fmt.Errorf("watch closed before LimaVM %q reached the desired state", key.Name)
}

// watchUntilDeleted watches a LimaVM until it is deleted.
func watchUntilDeleted(ctx context.Context, c client.WithWatch, limaVM *limav1alpha1.LimaVM) error {
	key := client.ObjectKeyFromObject(limaVM)

	var vm limav1alpha1.LimaVM
	err := c.Get(ctx, key, &vm)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if vm.UID != limaVM.UID {
		return nil
	}

	vmList := &limav1alpha1.LimaVMList{}
	watcher, err := c.Watch(ctx, vmList,
		client.InNamespace(key.Namespace),
		client.MatchingFields{"metadata.name": key.Name},
		&client.ListOptions{Raw: &metav1.ListOptions{ResourceVersion: vm.ResourceVersion}},
	)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for ev := range watcher.ResultChan() {
		switch ev.Type {
		case k8swatch.Deleted:
			return nil
		case k8swatch.Error:
			return apierrors.FromObject(ev.Object)
		}
	}
	// ResultChan closed. If ctx ended, return its error. Otherwise the
	// watch dropped; re-read and treat NotFound or a new UID as
	// confirmation of deletion.
	if err := ctx.Err(); err != nil {
		return err
	}
	var current limav1alpha1.LimaVM
	if err := c.Get(ctx, key, &current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if current.UID != limaVM.UID {
		return nil
	}
	return fmt.Errorf("watch closed before LimaVM %q was deleted", key.Name)
}

func limaVMDeleteAction(ctx context.Context, name string, wait bool, timeout time.Duration) error {
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
		waitCtx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
		defer cancel()
		if err := watchUntilDeleted(waitCtx, c, limaVM); err != nil {
			return cliexit.Classify(fmt.Errorf("failed to wait for LimaVM deletion: %w", err))
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

			return tail.File(cmd.Context(), cmd.OutOrStdout(), logPath, follow)
		},
	}
	command.Flags().BoolP("stdout", "o", false, "Print stdout instead of stderr")
	command.Flags().BoolP("follow", "f", false, "Follow log output")
	return command
}

// editorEnvs returns an ordered list of env vars to check for editor preferences.
func editorEnvs() []string {
	return []string{
		"KUBE_EDITOR",
		"EDITOR",
		"VISUAL",
	}
}
