// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// ctlAwaitAction waits for a resource condition to reach a specific state,
// optionally requiring the transition to have occurred after a given timestamp.
func ctlAwaitAction(cmd *cobra.Command, rawArgs []string) error {
	flags := pflag.NewFlagSet("await", pflag.ContinueOnError)
	forFlag := flags.String("for", "", "Condition to wait for: condition=TYPE[=STATUS] (STATUS defaults to True)")
	reason := flags.String("reason", "", "Required condition reason")
	since := flags.String("since", "", "Require lastTransitionTime after this value (ISO 8601 timestamp or \"startup\")")
	timeout := flags.Duration("timeout", 30*time.Second, "How long to wait")
	namespace := flags.StringP("namespace", "n", "default", "Resource namespace")

	if err := flags.Parse(rawArgs); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 1 {
		return fmt.Errorf("expected TYPE/NAME argument, got %d arguments", len(positional))
	}
	if *forFlag == "" {
		return errors.New("--for is required (e.g. --for=condition=Running)")
	}

	condType, condStatus, err := parseConditionFlag(*forFlag)
	if err != nil {
		return err
	}
	resourceType, resourceName, err := parseResourceArg(positional[0])
	if err != nil {
		return err
	}

	restConfig, err := service.GetKubeRestConfig()
	if err != nil {
		return err
	}

	// Resolve --since=startup to a concrete timestamp.
	var sinceTime time.Time
	if *since == "startup" {
		sinceTime, err = resolveStartupTime(cmd.Context(), restConfig)
		if err != nil {
			return fmt.Errorf("failed to resolve startup time: %w", err)
		}
	} else if *since != "" {
		sinceTime, err = time.Parse(time.RFC3339, *since)
		if err != nil {
			return fmt.Errorf("invalid --since timestamp %q: %w", *since, err)
		}
	}

	// Resolve resource type to a GVR using the discovery API.
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}
	gvr, err := resolveGVR(discoveryClient, resourceType)
	if err != nil {
		return err
	}

	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), *timeout)
	defer cancel()

	checker := conditionChecker{
		condType:   condType,
		condStatus: condStatus,
		reason:     *reason,
		sinceTime:  sinceTime,
	}

	return watchUntilCondition(ctx, dynClient, gvr, *namespace, resourceName, checker.check)
}

// parseConditionFlag parses "condition=TYPE[=STATUS]" from the --for flag.
func parseConditionFlag(flag string) (condType, condStatus string, err error) {
	prefix := "condition="
	if !strings.HasPrefix(flag, prefix) {
		return "", "", fmt.Errorf("--for must start with \"condition=\", got %q", flag)
	}
	value := flag[len(prefix):]
	parts := strings.SplitN(value, "=", 2)
	condType = parts[0]
	condStatus = "True"
	if len(parts) == 2 {
		condStatus = parts[1]
	}
	if condType == "" {
		return "", "", fmt.Errorf("condition type must not be empty in --for=%q", flag)
	}
	return condType, condStatus, nil
}

// parseResourceArg parses "TYPE[.GROUP]/NAME" into resource type and name.
func parseResourceArg(arg string) (resourceType, name string, err error) {
	slash := strings.IndexByte(arg, '/')
	if slash < 0 {
		return "", "", fmt.Errorf("expected TYPE/NAME, got %q", arg)
	}
	resourceType = arg[:slash]
	name = arg[slash+1:]
	if resourceType == "" || name == "" {
		return "", "", fmt.Errorf("both TYPE and NAME must be non-empty in %q", arg)
	}
	return resourceType, name, nil
}

// resolveGVR finds the GroupVersionResource for a resource type string like
// "limavm" or "limavm.lima.rancherdesktop.io".
func resolveGVR(client discovery.DiscoveryInterface, resourceType string) (schema.GroupVersionResource, error) {
	var resourceName, group string
	if dot := strings.IndexByte(resourceType, '.'); dot >= 0 {
		resourceName = resourceType[:dot]
		group = resourceType[dot+1:]
	} else {
		resourceName = resourceType
	}

	_, apiResourceLists, err := client.ServerGroupsAndResources()
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to discover API resources: %w", err)
	}

	for _, list := range apiResourceLists {
		gv, parseErr := schema.ParseGroupVersion(list.GroupVersion)
		if parseErr != nil {
			continue
		}
		if group != "" && gv.Group != group {
			continue
		}
		for _, r := range list.APIResources {
			if r.Name == resourceName || r.SingularName == resourceName {
				return schema.GroupVersionResource{
					Group:    gv.Group,
					Version:  gv.Version,
					Resource: r.Name,
				}, nil
			}
		}
	}
	return schema.GroupVersionResource{}, fmt.Errorf("resource type %q not found", resourceType)
}

// resolveStartupTime reads the earliest startTime from the controller manager
// discovery ConfigMap. The ConfigMap is guaranteed to be fresh because
// ensureServiceRunning waits for it after a restart.
func resolveStartupTime(ctx context.Context, config *rest.Config) (time.Time, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	cm, err := client.CoreV1().ConfigMaps(controllers.RDDSystemNamespace).Get(
		ctx, "rdd-controller-manager", metav1.GetOptions{},
	)
	if err != nil {
		return time.Time{}, err
	}

	var earliest time.Time
	for _, data := range cm.Data {
		var info controllers.ControllerManagerInfo
		if err := json.Unmarshal([]byte(data), &info); err != nil {
			continue
		}
		t := info.StartTime.Time
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	if earliest.IsZero() {
		return time.Time{}, errors.New("no controller manager entries in discovery ConfigMap")
	}
	return earliest, nil
}

type conditionChecker struct {
	condType   string
	condStatus string
	reason     string
	sinceTime  time.Time
}

func (c *conditionChecker) check(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, item := range conditions {
		cond, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := cond["type"].(string); typ != c.condType {
			continue
		}
		if status, _ := cond["status"].(string); status != c.condStatus {
			return false // Found the condition type but wrong status — keep waiting.
		}
		if c.reason != "" {
			if r, _ := cond["reason"].(string); r != c.reason {
				return false
			}
		}
		if !c.sinceTime.IsZero() {
			transitionStr, _ := cond["lastTransitionTime"].(string)
			transitionTime, parseErr := time.Parse(time.RFC3339, transitionStr)
			if parseErr != nil || !transitionTime.After(c.sinceTime) {
				return false
			}
		}
		return true
	}
	return false // Condition type not present yet.
}

// isResourceVersionStale returns true if the error indicates the resource
// version is too old. The API server returns 410 with reason "Gone" or
// "Expired" depending on the storage backend.
func isResourceVersionStale(err error) bool {
	return apierrors.IsGone(err) || apierrors.IsResourceExpired(err)
}

// watchUntilCondition watches a namespaced resource until the check function
// returns true. It reads the current state first, then watches from that
// resource version to avoid missing events. On 410 Gone/Expired (resource
// version too old), it retries the entire Get+Watch cycle.
func watchUntilCondition(
	ctx context.Context,
	client dynamic.Interface,
	gvr schema.GroupVersionResource,
	namespace, name string,
	check func(*unstructured.Unstructured) bool,
) error {
	resource := client.Resource(gvr).Namespace(namespace)

	for {
		obj, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get %s/%s: %w", gvr.Resource, name, err)
		}
		if check(obj) {
			return nil
		}

		retry, err := watchOnce(ctx, resource, name, obj.GetResourceVersion(), check)
		if err != nil {
			return err
		}
		if !retry {
			return ctx.Err()
		}
		// 410 Gone — resource version was compacted; restart the Get+Watch cycle.
	}
}

// watchOnce runs a single Watch from the given resource version. It returns
// (true, nil) if the watch expired with 410 Gone and should be retried.
func watchOnce(
	ctx context.Context,
	resource dynamic.ResourceInterface,
	name, resourceVersion string,
	check func(*unstructured.Unstructured) bool,
) (retry bool, err error) {
	watcher, err := resource.Watch(ctx, metav1.ListOptions{
		FieldSelector:   "metadata.name=" + name,
		ResourceVersion: resourceVersion,
	})
	if isResourceVersionStale(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to watch: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		switch event.Type {
		case watch.Error:
			if isResourceVersionStale(apierrors.FromObject(event.Object)) {
				return true, nil
			}
			return false, fmt.Errorf("watch error: %v", event.Object)
		case watch.Deleted:
			return false, errors.New("resource was deleted while waiting")
		}
		updated, ok := event.Object.(*unstructured.Unstructured)
		if ok && check(updated) {
			return false, nil
		}
	}
	return false, nil
}
