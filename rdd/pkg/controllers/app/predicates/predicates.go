// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

// Package predicates provides controller-runtime predicates for the App
// controllers.
package predicates

import (
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// WatchEventLogger returns a predicate that logs every watch event the
// controller receives, before workqueue dispatch. Logs at V(1), so default
// verbosity stays quiet.
func WatchEventLogger(name string) predicate.Predicate {
	log := logf.Log.WithName(name + ".watch")
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			log.V(1).Info("create",
				"name", e.Object.GetName(),
				"generation", e.Object.GetGeneration(),
				"resourceVersion", e.Object.GetResourceVersion(),
			)
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			log.V(1).Info("update",
				"name", e.ObjectNew.GetName(),
				"oldGeneration", e.ObjectOld.GetGeneration(),
				"newGeneration", e.ObjectNew.GetGeneration(),
				"oldResourceVersion", e.ObjectOld.GetResourceVersion(),
				"newResourceVersion", e.ObjectNew.GetResourceVersion(),
			)
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.V(1).Info("delete", "name", e.Object.GetName())
			return true
		},
		GenericFunc: func(e event.GenericEvent) bool {
			log.V(1).Info("generic", "name", e.Object.GetName())
			return true
		},
	}
}
