// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"context"
	"fmt"
	"reflect"

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
//
// Only fields that the API server accepts as CRD selectableFields (scalar
// string/boolean/integer fields) can be indexed this way; for more complex
// types, use [IndexField] directly with the desired JSONPath instead.
func IndexFields(ctx context.Context, obj client.Object, mgr ctrl.Manager) error {
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
			if err := IndexField(ctx, obj, mgr, field.JSONPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// IndexField registers a client-side field indexer for obj at the given
// JSONPath, independent of the object's CRD selectableFields.  This must be
// done per-process, as the field indexer is client-side.
//
// Use this (instead of relying on [IndexFields] and a
// `+kubebuilder:selectablefield` marker) for fields the API server would
// reject as a CRD selectableField, such as lists (possibly with +listMapKey
// markers).  `client.MatchingFields` queries still work against a client-side
// index like this even though it is never exposed as a `--field-selector`.
func IndexField(ctx context.Context, obj client.Object, mgr ctrl.Manager, jsonPath string) error {
	log := log.FromContext(ctx)
	jp := jsonpath.New(jsonPath)
	// jsonPath is a full JSONPath expression, including a leading dot (e.g.,
	// `.status.repoTag`).
	if err := jp.Parse("{" + jsonPath + "}"); err != nil {
		return fmt.Errorf("failed to parse field path %q for %T: %w", jsonPath, obj, err)
	}
	err := mgr.GetFieldIndexer().IndexField(
		ctx,
		obj,
		jsonPath,
		func(rawObj client.Object) []string {
			results, err := jp.FindResults(rawObj)
			if err != nil {
				log.V(3).Info("failed to extract field value", "field", jsonPath, "object", rawObj, "error", err)
				return nil
			}
			if len(results) == 0 {
				return nil
			}
			var values []string
			for _, res := range results {
				for _, value := range res {
					values = appendFieldValues(values, value)
				}
			}
			return values
		},
	)
	if err != nil {
		return fmt.Errorf("failed to index field %q for %T: %w", jsonPath, obj, err)
	}
	return nil
}

// appendFieldValues appends the string representation of value to values. If
// value is a slice or array (e.g. a JSONPath result that matched a whole
// map/slice-valued field), each element is appended individually rather than
// formatting the whole slice as one string, so that index lookups for a single
// element value succeed.
func appendFieldValues(values []string, value reflect.Value) []string {
	switch value.Kind() {
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			values = appendFieldValues(values, value.Index(i))
		}
		return values
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return values
		}
		return appendFieldValues(values, value.Elem())
	case reflect.Invalid:
		return values
	default:
		return append(values, fmt.Sprintf("%v", value))
	}
}
