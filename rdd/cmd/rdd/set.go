// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/component-base/term"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	cliexit "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/exit"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/cli/help"
	service "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/cmd"
)

const appCRDName = "apps.app.rancherdesktop.io"

var appGVR = schema.GroupVersionResource{
	Group:    appv1alpha1.GroupVersion.Group,
	Version:  appv1alpha1.GroupVersion.Version,
	Resource: "apps",
}

func newSetCommand() *cobra.Command {
	var (
		dryRun  bool
		wait    bool
		timeout time.Duration
	)
	command := &cobra.Command{
		Use:   "set PROPERTY=VALUE [PROPERTY=VALUE ...]",
		Short: "Set App configuration properties",
		Long: help.Doc(`
			Set one or more properties on the App singleton resource.

			Properties are specified as PROPERTY=VALUE pairs. Property names use
			dot notation for nested fields (e.g., containerEngine.name).

			Valid property names and types are derived from the App CRD at
			runtime. If the App resource does not exist, it is created with
			default settings before applying the specified values.

			By default, rdd set waits for the App's Settled condition
			to reach True at the new generation. The wait covers every
			property change and returns only after the full reconcile
			chain catches up. Pass --wait=false to return as soon as
			the patch is accepted, or --timeout=0 to wait indefinitely.

			Examples:
			  rdd set running=true
			  rdd set running=true containerEngine.name=containerd
			  rdd set kubernetes.enabled=true kubernetes.version=1.32.2
			  rdd set --dry-run running=true
			  rdd set --wait=false running=true
		`),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return setAction(cmd.Context(), args, dryRun, wait, timeout)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false,
		"Validate changes against the API server without persisting them")
	command.Flags().BoolVar(&wait, "wait", true,
		"Wait for the desired state after the patch is accepted")
	command.Flags().DurationVar(&timeout, "timeout", limaLongWaitTimeout,
		"Timeout for --wait; ignored if --wait=false (0 means wait indefinitely)")

	// Override help to append live property descriptions from the CRD schema.
	defaultHelp := command.HelpFunc()
	command.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if extra := fetchPropertyHelp(cmd.Context()); extra != "" {
			cmd.Long = cmd.Long + "\n" + extra
		}
		defaultHelp(cmd, args)
	})

	return command
}

// fetchPropertyHelp starts the service if needed, fetches the CRD schema, and
// returns a formatted list of available properties.
func fetchPropertyHelp(ctx context.Context) string {
	if err := ensureServiceRunning(ctx); err != nil {
		return ""
	}
	config, err := service.GetKubeRestConfig()
	if err != nil {
		return ""
	}
	schema, err := fetchSpecSchema(ctx, config)
	if err != nil {
		return ""
	}
	return formatPropertyHelp(schema, "")
}

// propertyHelpEntry holds one row of the property help table.
type propertyHelpEntry struct {
	path string
	info string
	desc string
}

// formatPropertyHelp returns a formatted table of settable properties with
// their types and descriptions, extracted from the CRD's OpenAPI schema.
func formatPropertyHelp(schema *apiextensionsv1.JSONSchemaProps, prefix string) string {
	var entries []propertyHelpEntry
	collectEntries(schema, prefix, &entries)
	if len(entries) == 0 {
		return ""
	}

	// Find the widest path and info columns for alignment.
	maxPath, maxInfo := 0, 0
	for _, e := range entries {
		if len(e.path) > maxPath {
			maxPath = len(e.path)
		}
		if len(e.info) > maxInfo {
			maxInfo = len(e.info)
		}
	}

	// Indent is "  " + path column + "  " + info column + "  ".
	indent := 2 + maxPath + 2 + maxInfo + 2
	totalWidth := 80
	if w, _, err := term.TerminalSize(os.Stdout); err == nil && w > 0 {
		totalWidth = w
	}
	descWidth := totalWidth - indent
	if descWidth < 20 {
		descWidth = 20
	}

	var b strings.Builder
	b.WriteString("Available properties:\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "  %-*s  %-*s  ", maxPath, e.path, maxInfo, e.info)
		writeWrapped(&b, e.desc, descWidth, indent)
	}
	return b.String()
}

// writeWrapped writes text word-wrapped to width, with subsequent lines
// indented by indent spaces. The first line is not indented (the caller
// already wrote the prefix). A trailing newline is always written.
func writeWrapped(b *strings.Builder, text string, width, indent int) {
	words := strings.Fields(text)
	if len(words) == 0 {
		b.WriteByte('\n')
		return
	}
	col := 0
	pad := strings.Repeat(" ", indent)
	for i, w := range words {
		wl := len(w)
		if i == 0 {
			b.WriteString(w)
			col = wl
		} else if col+1+wl > width {
			b.WriteByte('\n')
			b.WriteString(pad)
			b.WriteString(w)
			col = wl
		} else {
			b.WriteByte(' ')
			b.WriteString(w)
			col += 1 + wl
		}
	}
	b.WriteByte('\n')
}

// firstSentence returns the first sentence of a description, capitalized.
// It looks for ". " followed by an uppercase letter to avoid splitting on
// abbreviations like "e.g." or "i.e.".
func firstSentence(desc string) string {
	for i := range len(desc) - 2 {
		if desc[i] == '.' && desc[i+1] == ' ' && desc[i+2] >= 'A' && desc[i+2] <= 'Z' {
			desc = desc[:i+1]
			break
		}
	}
	if desc != "" {
		desc = strings.ToUpper(desc[:1]) + desc[1:]
	}
	return desc
}

// collectEntries recursively collects leaf properties from the schema.
func collectEntries(schema *apiextensionsv1.JSONSchemaProps, prefix string, out *[]propertyHelpEntry) {
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		prop := schema.Properties[name]
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "." + name
		}
		if prop.Type == "object" && len(prop.Properties) > 0 {
			*out = append(*out, propertyHelpEntry{fullPath, "(object)", firstSentence(prop.Description)})
			collectEntries(&prop, fullPath, out)
			continue
		}

		info := "(" + prop.Type + ")"
		if len(prop.Enum) > 0 {
			var values []string
			for _, e := range prop.Enum {
				var s string
				if json.Unmarshal(e.Raw, &s) == nil {
					values = append(values, s)
				}
			}
			if len(values) > 0 {
				info = "(" + strings.Join(values, "|") + ")"
			}
		}

		*out = append(*out, propertyHelpEntry{fullPath, info, firstSentence(prop.Description)})
	}
}

func setAction(ctx context.Context, args []string, dryRun, wait bool, timeout time.Duration) error {
	// Parse PROPERTY=VALUE arguments.
	properties := make(map[string]string, len(args))
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("invalid argument %q: expected PROPERTY=VALUE format", arg)
		}
		if key == "" {
			return fmt.Errorf("invalid argument %q: property name must not be empty", arg)
		}
		properties[key] = value
	}

	c, restConfig, err := getAppKubeClient(ctx)
	if err != nil {
		return err
	}

	// Fetch the CRD schema and validate/coerce all property values.
	specSchema, err := fetchSpecSchema(ctx, restConfig)
	if err != nil {
		return err
	}

	coerced := make(map[string]any, len(properties))
	for path, rawValue := range properties {
		segments := strings.Split(path, ".")
		leafSchema, err := resolvePropertyPath(specSchema, segments)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		value, err := coerceValue(leafSchema, rawValue)
		if err != nil {
			return fmt.Errorf("invalid value %q for %q: %w", rawValue, path, err)
		}
		coerced[path] = value
	}

	specMap := buildNestedMap(coerced)

	// Get-or-create the App, then patch with the specified values.
	// Capture the post-write generation so waitForDesiredState can
	// reject stale condition snapshots from a previous reconcile.
	var (
		app       appv1alpha1.App
		targetGen int64
	)
	err = c.Get(ctx, client.ObjectKey{Name: "app"}, &app)
	switch {
	case apierrors.IsNotFound(err):
		gen, err := createAndPatchApp(ctx, c, restConfig, specMap, specSchema, dryRun)
		if err != nil {
			return classifyAPIError(err)
		}
		targetGen = gen
	case err != nil:
		return fmt.Errorf("failed to get App: %w", err)
	default:
		gen, err := patchApp(ctx, c, &app, specMap, dryRun)
		if err != nil {
			return classifyAPIError(err)
		}
		targetGen = gen
	}

	if dryRun || !wait {
		return nil
	}
	if err := waitForDesiredState(ctx, restConfig, timeout, targetGen); err != nil {
		return cliexit.Classify(err)
	}
	return nil
}

// classifyAPIError tags admission-controller rejections with
// [cliexit.CodeRejected]. Other API errors pass through unchanged.
func classifyAPIError(err error) error {
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) || apierrors.IsForbidden(err) {
		return cliexit.Rejected(err)
	}
	return err
}

// createAndPatchApp creates the App using the dynamic client so that
// only user-specified fields (plus required fields with zero values)
// are sent; the API server fills in CRD defaults. The returned
// generation is metadata.generation after the write, used to filter
// stale condition snapshots in the post-write wait.
//
// In dry-run mode the create uses only required defaults so the App
// exists for admission validation; a follow-up dry-run patch then
// carries the user's values.
func createAndPatchApp(ctx context.Context, c client.Client, config *rest.Config, specMap map[string]any, specSchema *apiextensionsv1.JSONSchemaProps, dryRun bool) (int64, error) {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return 0, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	createSpec := specMap
	if dryRun {
		// Create with required defaults only; patch carries the real values.
		createSpec = make(map[string]any)
	}
	fillRequiredFields(createSpec, specSchema)

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": appv1alpha1.GroupVersion.String(),
			"kind":       appv1alpha1.AppKind,
			"metadata":   map[string]any{"name": "app"},
			"spec":       createSpec,
		},
	}

	created, err := dynClient.Resource(appGVR).Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		// Race: another rdd set created it concurrently. Retry as patch.
		var app appv1alpha1.App
		if err := c.Get(ctx, client.ObjectKey{Name: "app"}, &app); err != nil {
			return 0, fmt.Errorf("failed to get App after concurrent create: %w", err)
		}
		return patchApp(ctx, c, &app, specMap, dryRun)
	}
	if err != nil {
		return 0, fmt.Errorf("failed to create App: %w", err)
	}

	if dryRun {
		logrus.Info("App created with defaults")
		var app appv1alpha1.App
		if err := c.Get(ctx, client.ObjectKey{Name: "app"}, &app); err != nil {
			return 0, fmt.Errorf("failed to get App: %w", err)
		}
		return patchApp(ctx, c, &app, specMap, true)
	}

	logrus.Info("App created")
	return created.GetGeneration(), nil
}

// fillRequiredFields adds zero values for required fields that are missing
// from specMap, so the API server does not reject the create request. It
// recurses into nested objects that already exist in specMap.
func fillRequiredFields(specMap map[string]any, schema *apiextensionsv1.JSONSchemaProps) {
	for _, name := range schema.Required {
		if _, ok := specMap[name]; ok {
			continue
		}
		prop, ok := schema.Properties[name]
		if !ok {
			continue
		}
		switch prop.Type {
		case "boolean":
			specMap[name] = false
		case "integer":
			specMap[name] = int64(0)
		case "string":
			if len(prop.Enum) == 0 {
				specMap[name] = ""
			} else {
				var first string
				if json.Unmarshal(prop.Enum[0].Raw, &first) == nil {
					specMap[name] = first
				}
			}
		}
	}
	for name, val := range specMap {
		nested, ok := val.(map[string]any)
		if !ok {
			continue
		}
		prop, ok := schema.Properties[name]
		if ok && prop.Type == "object" {
			fillRequiredFields(nested, &prop)
		}
	}
}

// patchApp applies a merge patch with the given spec properties. In
// dry-run mode the API server validates without persisting. The
// returned generation is metadata.generation after the patch, used to
// filter stale condition snapshots in the post-write wait.
func patchApp(ctx context.Context, c client.Client, app *appv1alpha1.App, specMap map[string]any, dryRun bool) (int64, error) {
	patchObj := map[string]any{"spec": specMap}
	patchBytes, err := json.Marshal(patchObj)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal patch: %w", err)
	}

	var opts []client.PatchOption
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}

	if err := c.Patch(ctx, app, client.RawPatch(types.MergePatchType, patchBytes), opts...); err != nil {
		return 0, fmt.Errorf("failed to update App: %w", err)
	}

	if dryRun {
		logrus.Info("App validated (dry run)")
	} else {
		logrus.Info("App updated")
	}
	return app.Generation, nil
}

// waitForDesiredState waits for the App reconcile chain to settle on
// the new spec. Every property change waits for Settled=True with
// ObservedGeneration >= minGen. Settled gates on the LimaVM running the
// current template (a spec change that rewrites the template holds
// Settled False until the VM restarts into it), on the LimaVM reaching
// its terminal phase (Started or Stopped), on ContainerEngineReady=True
// at the current generation when the engine controller is registered,
// and on KubernetesReady=True at the current generation when
// spec.kubernetes.enabled is true.
//
// Filtering on ObservedGeneration >= minGen blocks a stale
// Settled=True from a prior reconcile from satisfying the wait
// before the App controller observes our write.
func waitForDesiredState(ctx context.Context, config *rest.Config, timeout time.Duration, minGen int64) error {
	// timeout == 0 means "no deadline", matching kubectl wait.
	ctx, cancel := watchtools.ContextWithOptionalTimeout(ctx, timeout)
	defer cancel()

	logrus.Info("Waiting for App to settle")
	reporter := newConditionReporter(minGen)
	return watchCondition(ctx, config, func(obj *unstructured.Unstructured) bool {
		// Surface reconcile progress: log each App condition as its
		// status, reason, or message changes while we wait.
		reporter.report(obj)
		status, gen, present := conditionInfo(obj, appv1alpha1.AppConditionSettled)
		return present && gen >= minGen && status == metav1.ConditionTrue
	})
}

// conditionReporter logs App status condition changes as they happen, so
// `rdd set --wait` shows reconcile progress instead of blocking silently. It
// reports only conditions observed at or after the target generation, so the
// output tracks this change rather than a stale snapshot from an earlier spec
// version — the same generation filter waitForDesiredState applies to Settled.
type conditionReporter struct {
	minGen int64
	seen   map[string]string
}

func newConditionReporter(minGen int64) *conditionReporter {
	return &conditionReporter{minGen: minGen, seen: make(map[string]string)}
}

// report logs each App condition at or after the target generation whose
// status, reason, or message changed since the last snapshot.
func (r *conditionReporter) report(obj *unstructured.Unstructured) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		observedGen, _, _ := unstructured.NestedInt64(cond, "observedGeneration")
		if observedGen < r.minGen {
			continue // Stale snapshot from an earlier spec version.
		}
		condType, _ := cond["type"].(string)
		status, _ := cond["status"].(string)
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		key := status + "|" + reason + "|" + message
		if r.seen[condType] == key {
			continue
		}
		r.seen[condType] = key
		logrus.Infof("%s=%s: %s (%s)", condType, status, message, reason)
	}
}

// watchCondition watches the App singleton until the predicate
// returns true or the context expires. watchtools.UntilWithSync
// re-lists transparently on benign watch-channel closures (API
// timeouts, proxy disconnects, resource-version compaction).
func watchCondition(ctx context.Context, config *rest.Config, satisfied func(*unstructured.Unstructured) bool) error {
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	// Defensive filter; the webhook enforces metadata.name=app on the singleton.
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = "metadata.name=app"
			return dynClient.Resource(appGVR).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = "metadata.name=app"
			return dynClient.Resource(appGVR).Watch(ctx, options)
		},
	}

	precondition := func(store cache.Store) (bool, error) {
		for _, item := range store.List() {
			if obj, ok := item.(*unstructured.Unstructured); ok && satisfied(obj) {
				return true, nil
			}
		}
		return false, nil
	}

	condition := func(event watch.Event) (bool, error) {
		obj, ok := event.Object.(*unstructured.Unstructured)
		if !ok {
			return false, nil
		}
		return satisfied(obj), nil
	}

	if _, err := watchtools.UntilWithSync(ctx, lw, &unstructured.Unstructured{}, precondition, condition); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out waiting for App state: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("wait cancelled: %w", err)
		}
		return fmt.Errorf("failed to watch App: %w", err)
	}
	return nil
}

// conditionInfo returns the status, observedGeneration, and presence
// of the named condition on an App. Callers must check present
// before trusting the other return values.
func conditionInfo(obj *unstructured.Unstructured, condType string) (status metav1.ConditionStatus, observedGen int64, present bool) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "", 0, false
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] != condType {
			continue
		}
		s, _ := cond["status"].(string)
		gen, _, _ := unstructured.NestedInt64(cond, "observedGeneration")
		return metav1.ConditionStatus(s), gen, true
	}
	return "", 0, false
}

// getAppKubeClient returns a controller-runtime client with the App scheme
// registered, and the raw REST config for creating other clients.
func getAppKubeClient(ctx context.Context) (client.Client, *rest.Config, error) {
	if err := ensureServiceRunning(ctx); err != nil {
		return nil, nil, err
	}
	config, err := service.GetKubeRestConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}
	runtimeScheme := runtime.NewScheme()
	if err := appv1alpha1.AddToScheme(runtimeScheme); err != nil {
		return nil, nil, fmt.Errorf("failed to add App types to scheme: %w", err)
	}
	c, err := client.New(config, client.Options{Scheme: runtimeScheme})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client: %w", err)
	}
	return c, config, nil
}

// fetchSpecSchema retrieves the App CRD from the API server and returns the
// OpenAPI v3 schema for the spec field.
func fetchSpecSchema(ctx context.Context, config *rest.Config) (*apiextensionsv1.JSONSchemaProps, error) {
	client, err := apiextensionsclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create apiextensions client: %w", err)
	}

	crd, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, appCRDName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, errors.New("app CRD is not installed; make sure the control plane is running with the app controller enabled")
		}
		return nil, fmt.Errorf("failed to fetch App CRD: %w", err)
	}

	// Find the storage version's schema.
	for i := range crd.Spec.Versions {
		v := &crd.Spec.Versions[i]
		if !v.Storage {
			continue
		}
		if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			return nil, fmt.Errorf("app CRD version %s has no OpenAPI schema", v.Name)
		}
		spec, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]
		if !ok {
			return nil, fmt.Errorf("app CRD version %s has no spec field in schema", v.Name)
		}
		// namespace is immutable after creation and not settable via "rdd set".
		delete(spec.Properties, "namespace")
		return &spec, nil
	}
	return nil, errors.New("app CRD has no storage version")
}

// resolvePropertyPath walks the schema tree along dot-separated path segments
// and returns the leaf schema node. Returns an error with valid property names
// if the path is invalid.
func resolvePropertyPath(schema *apiextensionsv1.JSONSchemaProps, segments []string) (*apiextensionsv1.JSONSchemaProps, error) {
	current := schema
	for i, segment := range segments {
		prop, ok := current.Properties[segment]
		if !ok {
			path := strings.Join(segments[:i], ".")
			validNames := listProperties(current, "")
			if path != "" {
				return nil, fmt.Errorf("unknown property %q under %q; valid properties: %s",
					segment, path, strings.Join(validNames, ", "))
			}
			return nil, fmt.Errorf("unknown property %q; valid properties: %s",
				segment, strings.Join(listProperties(schema, ""), ", "))
		}
		if i < len(segments)-1 {
			if prop.Type != "object" {
				fullPath := strings.Join(segments[:i+1], ".")
				return nil, fmt.Errorf("%q is not an object and has no sub-properties", fullPath)
			}
			current = &prop
			continue
		}
		// Last segment: reject object-typed targets.
		if prop.Type == "object" {
			fullPath := strings.Join(segments, ".")
			children := listProperties(&prop, "")
			if len(children) > 0 {
				return nil, fmt.Errorf("%q is an object; set its fields, e.g. %q",
					fullPath, fullPath+"."+children[0])
			}
			return nil, fmt.Errorf("%q is an object, not a settable property", fullPath)
		}
		return &prop, nil
	}
	return nil, errors.New("empty property path")
}

// coerceValue converts a raw string to the Go type indicated by the schema.
func coerceValue(schema *apiextensionsv1.JSONSchemaProps, raw string) (any, error) {
	// An empty string clears the field. For string types this sets it to "",
	// which omitempty treats as unset. For boolean/integer types the
	// type-specific parsers below return a clear error.
	if len(schema.Enum) > 0 {
		var validValues []string
		found := false
		for _, e := range schema.Enum {
			var s string
			if err := json.Unmarshal(e.Raw, &s); err != nil {
				continue
			}
			validValues = append(validValues, s)
			if s == raw {
				found = true
			}
		}
		if !found && len(validValues) > 0 {
			return nil, fmt.Errorf("valid values: %s", strings.Join(validValues, ", "))
		}
	}

	switch schema.Type {
	case "boolean":
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, errors.New("expected boolean; use \"true\" or \"false\"")
		}
		return v, nil
	case "integer":
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New("expected integer")
		}
		return v, nil
	case "string":
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported schema type %q", schema.Type)
	}
}

// buildNestedMap converts a flat map of dotted paths to values into a nested
// map structure. For example, {"a.b": 1, "a.c": 2} becomes {"a": {"b": 1, "c": 2}}.
func buildNestedMap(flat map[string]any) map[string]any {
	result := make(map[string]any)
	for path, value := range flat {
		segments := strings.Split(path, ".")
		current := result
		for _, segment := range segments[:len(segments)-1] {
			next, ok := current[segment].(map[string]any)
			if !ok {
				next = make(map[string]any)
				current[segment] = next
			}
			current = next
		}
		current[segments[len(segments)-1]] = value
	}
	return result
}

// listProperties returns all settable (leaf) property paths under the given
// schema node, sorted alphabetically.
func listProperties(schema *apiextensionsv1.JSONSchemaProps, prefix string) []string {
	var paths []string
	for name, prop := range schema.Properties {
		fullPath := name
		if prefix != "" {
			fullPath = prefix + "." + name
		}
		if prop.Type == "object" && len(prop.Properties) > 0 {
			paths = append(paths, listProperties(&prop, fullPath)...)
		} else {
			paths = append(paths, fullPath)
		}
	}
	sort.Strings(paths)
	return paths
}
