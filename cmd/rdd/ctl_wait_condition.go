// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

// ctlWaitConditionAction waits for a resource condition to reach a specific state,
// optionally requiring the transition to have occurred after a given timestamp.
func ctlWaitConditionAction(cmd *cobra.Command, rawArgs []string) error {
	flags := pflag.NewFlagSet("wait-condition", pflag.ContinueOnError)
	reason := flags.String("reason", "", "Required condition reason")
	since := flags.String("since", "", "Require lastTransitionTime after this value (ISO 8601 timestamp or \"startup\")")
	timeout := flags.Duration("timeout", 30*time.Second, "How long to wait")
	namespace := flags.StringP("namespace", "n", "default", "Resource namespace")

	if err := flags.Parse(rawArgs); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 2 {
		return fmt.Errorf("expected TYPE/NAME and CONDITION[=STATUS] arguments, got %d arguments", len(positional))
	}

	resourceType, resourceName, err := parseResourceArg(positional[0])
	if err != nil {
		return err
	}
	condType, condStatus, err := parseConditionArg(positional[1])
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
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	typeName, group, _ := strings.Cut(resourceType, ".")
	gvr, err := mapper.ResourceFor(schema.GroupVersionResource{Resource: typeName, Group: group})
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

	// Watch the single named resource for condition changes.
	// UntilWithSync handles the initial List, Watch setup, and 410 Gone
	// recovery (re-list on compacted resource versions).
	resource := dynClient.Resource(gvr).Namespace(*namespace)
	fieldSelector := "metadata.name=" + resourceName
	lw := &cache.ListWatch{
		ListFunc: func(opts metav1.ListOptions) (runtime.Object, error) {
			opts.FieldSelector = fieldSelector
			return resource.List(ctx, opts)
		},
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			return resource.Watch(ctx, opts)
		},
	}
	_, err = watchtools.UntilWithSync(ctx, lw, &unstructured.Unstructured{}, nil,
		func(event watch.Event) (bool, error) {
			if event.Type == watch.Deleted {
				return false, errors.New("resource was deleted while waiting")
			}
			obj, ok := event.Object.(*unstructured.Unstructured)
			if !ok {
				return false, nil
			}
			return checker.check(obj), nil
		},
	)
	return err
}

// parseConditionArg parses "TYPE[=STATUS]" into condition type and status.
// STATUS defaults to "True" when omitted.
func parseConditionArg(arg string) (condType, condStatus string, err error) {
	condType, condStatus, _ = strings.Cut(arg, "=")
	if condStatus == "" {
		condStatus = "True"
	}
	if condType == "" {
		return "", "", fmt.Errorf("condition type must not be empty in %q", arg)
	}
	return condType, condStatus, nil
}

// parseResourceArg parses "TYPE[.GROUP]/NAME" into resource type and name.
func parseResourceArg(arg string) (resourceType, name string, err error) {
	resourceType, name, found := strings.Cut(arg, "/")
	if !found {
		return "", "", fmt.Errorf("expected TYPE/NAME, got %q", arg)
	}
	if resourceType == "" || name == "" {
		return "", "", fmt.Errorf("both TYPE and NAME must be non-empty in %q", arg)
	}
	return resourceType, name, nil
}

// resolveStartupTime returns the control plane start time from the discovery
// ConfigMap's creationTimestamp. The serve command recreates the ConfigMap on
// every startup, so its creation time reflects the current instance.
func resolveStartupTime(ctx context.Context, config *rest.Config) (time.Time, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	cm, err := client.CoreV1().ConfigMaps(controllers.RDDSystemNamespace).Get(
		ctx, controllers.ControllerManagerConfigMapName, metav1.GetOptions{},
	)
	if err != nil {
		return time.Time{}, err
	}
	return cm.CreationTimestamp.Time, nil
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
