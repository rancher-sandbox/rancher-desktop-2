// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// ExtensionExtractedReconciler reconciles an Extension object's Extracted
// condition.
type ExtensionExtractedReconciler struct {
	// extensionExtractor handles the actual extraction logic for the extension.
	*extensionExtractor

	// engine is the container engine currently available for extraction, or
	// nil if none is available.
	engine engine
	// engineMu protects access to the engine field.
	engineMu sync.Mutex
}

// NewExtensionExtractedReconciler creates a new ExtensionExtractedReconciler.
func NewExtensionExtractedReconciler(ctx context.Context, mgr ctrl.Manager) *ExtensionExtractedReconciler {
	extractor := &extensionExtractor{
		Client: mgr.GetClient(),
		ctx:    ctx,
		state:  make(map[types.UID]extractState),
	}
	extractor.prepare = extractor.prepareImpl
	extractor.extractMetadata = extractor.extractMetadataImpl
	extractor.extractIcon = extractor.extractIconImpl
	extractor.extractUI = extractor.extractUIImpl
	extractor.extractExecutable = extractor.extractExecutableImpl
	extractor.reconcileDelete = extractor.reconcileDeleteImpl
	extractor.extractStep = extractor.extractStepImpl
	return &ExtensionExtractedReconciler{
		extensionExtractor: extractor,
	}
}

// extractedFinalizer is the finalizer used to ensure that any resources
// associated with an extension extraction is cleaned up.
const extractedFinalizer = "extensions.rancherdesktop.io/extracted"

type extensionExtractedReconcileRequest struct {
	kind string
	types.NamespacedName
}

var _ reconcile.TypedReconciler[extensionExtractedReconcileRequest] = &ExtensionExtractedReconciler{}

// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions/status,verbs=get;update;patch

// Reconcile implements the [reconcile.TypedReconciler] interface for the ExtensionExtractedReconciler.
func (r *ExtensionExtractedReconciler) Reconcile(ctx context.Context, req extensionExtractedReconcileRequest) (ctrl.Result, error) {
	switch req.kind {
	case appv1alpha1.AppKind:
		var app appv1alpha1.App
		err := r.Get(ctx, req.NamespacedName, &app)
		if apierrors.IsNotFound(err) {
			return r.reconcileApp(ctx, nil)
		} else if err != nil {
			return ctrl.Result{}, err
		}
		return r.reconcileApp(ctx, &app)
	default:
		var extension v1alpha1.Extension
		if err := r.Get(ctx, req.NamespacedName, &extension); err != nil {
			return ctrl.Result{}, err
		}
		return r.reconcileExtension(ctx, &extension)
	}
}

func (r *ExtensionExtractedReconciler) reconcileApp(ctx context.Context, app *appv1alpha1.App) (ctrl.Result, error) {
	// Update the engine reference.
	var engine engine
	switch {
	case app == nil:
	case !apimeta.IsStatusConditionTrue(app.Status.Conditions, appv1alpha1.AppConditionContainerEngineReady):
	case app.Spec.ContainerEngine.Name == "moby":
		engine = &dockerEngine{}
	case app.Spec.ContainerEngine.Name == "containerd":
		return ctrl.Result{}, errors.New("not implemented: containerd engine")
	}

	var err error
	if engine != nil {
		if err = engine.connect(ctx); err != nil {
			engine = nil
		}
	}
	r.engineMu.Lock()
	r.engine = engine
	r.engineMu.Unlock()
	if err != nil {
		// If there was a connect failure, return the error.
		return ctrl.Result{}, err
	}

	// Update extensions to note the container engines are ready.
	var extensions v1alpha1.ExtensionList
	if err := r.List(ctx, &extensions); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if len(extensions.Items) < 1 {
		return ctrl.Result{}, nil
	}

	var errs []error
	for _, ext := range extensions.Items {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest v1alpha1.Extension
			if err := r.Get(ctx, client.ObjectKeyFromObject(&ext), &latest); err != nil {
				return client.IgnoreNotFound(err)
			}

			condition := metav1.Condition{
				Type:    v1alpha1.ExtensionConditionContainerEngineReady,
				Status:  metav1.ConditionFalse,
				Reason:  v1alpha1.ExtensionContainerEngineReadyReasonNotReady,
				Message: "The app does not exist",
			}
			if engine != nil {
				condition.Status = metav1.ConditionTrue
				condition.Reason = v1alpha1.ExtensionContainerEngineReadyReasonReady
				condition.Message = fmt.Sprintf("The %s engine is ready", app.Spec.ContainerEngine.Name)
			}
			if apimeta.SetStatusCondition(&latest.Status.Conditions, condition) {
				return r.Status().Update(ctx, &latest)
			}
			return nil
		})
		errs = append(errs, err)
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// reconcileExtension handles the reconciliation of the Extension resource's Extracted condition.
func (r *ExtensionExtractedReconciler) reconcileExtension(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Extension Extracted condition",
		"name", ext.Name, "namespace", ext.Namespace)

	if base.IsBeingDeleted(ext) {
		return r.reconcileDelete(ctx, ext)
	}

	if !controllerutil.ContainsFinalizer(ext, extractedFinalizer) {
		return ctrl.Result{}, base.AddFinalizerWithRetry(ctx, r.extensionExtractor, ext, extractedFinalizer)
	}

	condition := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionExtracted)
	r.engineMu.Lock()
	engine := r.engine
	r.engineMu.Unlock()

	switch {
	// === states that do not require further processing ===
	case condition == nil:
		// The extension does not require extraction yet.
		return ctrl.Result{}, nil
	case condition.Reason == v1alpha1.ExtensionExtractedReasonCompleted:
		if condition.Status == metav1.ConditionTrue {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionTrue,
			v1alpha1.ExtensionExtractedReasonCompleted, "Extraction completed successfully")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonFailed:
		// Do nothing; the message has already been set.
		return ctrl.Result{}, nil
	// === states that require further processing ===
	case engine == nil:
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonEngineNotReady, "The container engine is not ready")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonEngineNotReady:
		// Given the previous branch, the engine is ready.
		return ctrl.Result{}, r.setExtractedCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionExtractedReasonPreparing, "The container engine became ready")
	case condition.Reason == v1alpha1.ExtensionExtractedReasonPreparing:
		return r.prepare(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonMetadata:
		return r.extractMetadata(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonIcon:
		return r.extractIcon(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonUI:
		return r.extractUI(ctx, ext, engine)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonExecutable:
		return r.extractExecutable(ctx, ext, engine)
	default:
		// Should not be reachable.
		log.Error(errors.New("unexpected condition"),
			"unexpected extraction condition reason",
			"reason", condition.Reason)
		return ctrl.Result{}, fmt.Errorf("unexpected extraction condition reason: %s", condition.Reason)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExtensionExtractedReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return builder.TypedControllerManagedBy[extensionExtractedReconcileRequest](mgr).
		Watches(&v1alpha1.Extension{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []extensionExtractedReconcileRequest {
			return []extensionExtractedReconcileRequest{{
				kind:           v1alpha1.ExtensionKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
			}}
		})).
		Watches(&appv1alpha1.App{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []extensionExtractedReconcileRequest {
			return []extensionExtractedReconcileRequest{{
				kind:           appv1alpha1.AppKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
			}}
		})).
		Named("extension-extracted-reconciler").
		Complete(r)
}
