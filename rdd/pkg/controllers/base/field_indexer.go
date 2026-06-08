// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/client-go/util/jsonpath"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// IndexFields configures the field indexer for CRD objects based on the
// `+kubebuilder:selectablefield` markers in the CRD object definition.
// This must be done per-process, as the field indexer is client-side.
func IndexFields(ctx context.Context, obj client.Object, mgr ctrl.Manager) error {
	log := log.FromContext(ctx)
	gvk, err := apiutil.GVKForObject(obj, mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("failed to get GVK for %T: %w", obj, err)
	}
	mapping, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("failed to get REST mapping for %T: %w", obj, err)
	}
	var crd apiextensionsv1.CustomResourceDefinition
	// The client-side cache is typically not set up at this point, so we need to
	// use .GetAPIReader() instead of .GetClient().
	err = mgr.GetAPIReader().Get(ctx, client.ObjectKey{Name: mapping.Resource.Resource + "." + gvk.Group}, &crd)
	if err != nil {
		return fmt.Errorf("failed to get CRD for %T: %w", obj, err)
	}

	for _, version := range crd.Spec.Versions {
		if !version.Served {
			continue
		}
		for _, field := range version.SelectableFields {
			if field.JSONPath == "" {
				continue
			}
			jp := jsonpath.New(field.JSONPath)
			// field.JSONPath is a full JSONPath expression, including a leading
			// dot (e.g., `.status.repoTag`).
			if err := jp.Parse("{" + field.JSONPath + "}"); err != nil {
				return fmt.Errorf("failed to parse selectableField %q for %T: %w", field.JSONPath, obj, err)
			}
			err := mgr.GetFieldIndexer().IndexField(
				ctx,
				obj,
				field.JSONPath,
				func(rawObj client.Object) []string {
					results, err := jp.FindResults(rawObj)
					if err != nil {
						log.V(3).Info("failed to extract field value", "field", field.JSONPath, "object", rawObj, "error", err)
						return nil
					}
					if len(results) == 0 {
						return nil
					}
					var values []string
					for _, res := range results {
						for _, value := range res {
							values = append(values, fmt.Sprintf("%v", value))
						}
					}
					return values
				},
			)
			if err != nil {
				return fmt.Errorf("failed to index field %q for %T: %w", field.JSONPath, obj, err)
			}
		}
	}
	return nil
}
