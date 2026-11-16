// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
)

// reconcileStarted computes and applies the Started condition for ext.
func (r *ExtensionReconciler) reconcileStarted(ctx context.Context, ext *v1alpha1.Extension) error {
	// Don't bother doing anything until we're installed.
	if !apimeta.IsStatusConditionTrue(ext.Status.Conditions, v1alpha1.ExtensionConditionInstalled) {
		return r.reconcileNotInstalled(ctx, ext)
	}

	// Check if we actually have anything to start
	manifest, err := getManifest(ext)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to get extension manifest")
		return err
	}
	if manifest == nil {
		logf.FromContext(ctx).Error(nil, "Extension metadata is not available")
		return errors.New("extension metadata is not available")
	}
	if manifest.VM.Image == "" && manifest.VM.ComposeFile == "" {
		// Nothing to start.
		return r.setCondition(ctx, ext,
			v1alpha1.ExtensionConditionStarted,
			metav1.ConditionTrue,
			v1alpha1.ExtensionStartedReasonStarted,
			"Extension has nothing to start")
	}

	var compose containersv1alpha1.ComposeProject
	err = r.Get(ctx, composeKeyForExtension(ext), &compose)
	if client.IgnoreNotFound(err) != nil {
		logf.FromContext(ctx).Error(err, "Failed to get Compose object for Extension")
		return err
	}

	if err != nil {
		// Compose object does not exist.
		return r.reconcileComposeMissing(ctx, ext, composeProjectNameForExtension(ext))
	}

	// Compose object exists; make sure we have a owner reference on it.
	hasOwnerRef, err := controllerutil.HasOwnerReference(compose.GetOwnerReferences(), ext, r.Scheme())
	if err != nil {
		return err
	}
	if !hasOwnerRef {
		err = controllerutil.SetOwnerReference(
			ext,
			&compose,
			r.Scheme(),
			controllerutil.WithBlockOwnerDeletion(true))
		if err != nil {
			logf.FromContext(ctx).Error(err, "Failed to set owner reference on Compose object")
			return err
		}
		if err := r.Update(ctx, &compose); err != nil {
			return err
		}
	}
	// Everything is good; set the Started condition to true.
	return r.setCondition(ctx, ext,
		v1alpha1.ExtensionConditionStarted,
		metav1.ConditionTrue,
		v1alpha1.ExtensionStartedReasonStarted,
		"Extension has started")
}

// reconcileDeleteStarted handles stopping compose workloads when ext is deleted.
// It returns true if workloads are still being stopped, or false if fully stopped.
func (r *ExtensionReconciler) reconcileDeleteStarted(ctx context.Context, ext *v1alpha1.Extension) (bool, error) {
	setStatusCondition := apimeta.SetStatusCondition(&ext.Status.Conditions, metav1.Condition{
		Type:    v1alpha1.ExtensionConditionStarted,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.ExtensionStartedReasonStopping,
		Message: "Extension is stopping",
	})

	var req containersv1alpha1.ComposeUpRequest
	composeKey := composeKeyForExtension(ext)
	err := r.Get(ctx, composeKey, &req)
	if client.IgnoreNotFound(err) != nil {
		return true, err
	} else if err == nil {
		if setStatusCondition {
			if err := r.Status().Update(ctx, ext); err != nil {
				return true, client.IgnoreNotFound(err)
			}
		}
		return true, client.IgnoreNotFound(r.Delete(ctx, &req))
	}

	var compose containersv1alpha1.ComposeProject
	err = r.Get(ctx, composeKey, &compose)
	if client.IgnoreNotFound(err) != nil {
		return true, err
	} else if err == nil {
		if setStatusCondition {
			if err := r.Status().Update(ctx, ext); err != nil {
				return true, client.IgnoreNotFound(err)
			}
		}
		return true, client.IgnoreNotFound(r.Delete(ctx, &compose))
	}

	// Both Compose and ComposeUpRequest are gone; set the status to Stopped.
	return false, r.setCondition(ctx, ext,
		v1alpha1.ExtensionConditionStarted,
		metav1.ConditionFalse,
		v1alpha1.ExtensionStartedReasonStopped,
		"Extension has stopped")
}

// reconcileNotInstalled sets the Started condition to reflect that the
// extension is not yet installed, so it cannot be started.
func (r *ExtensionReconciler) reconcileNotInstalled(ctx context.Context, ext *v1alpha1.Extension) error {
	return r.setCondition(ctx, ext,
		v1alpha1.ExtensionConditionStarted,
		metav1.ConditionFalse,
		v1alpha1.ExtensionStartedReasonInstalling,
		"Extension is not installed, so it cannot be started")
}

// reconcileComposeMissing handles the case where the Compose object does not
// exist yet: it looks up (or creates) the corresponding ComposeUpRequest and
// reflects its progress via the Started condition.
func (r *ExtensionReconciler) reconcileComposeMissing(ctx context.Context, ext *v1alpha1.Extension, projectName string) error {
	log := logf.FromContext(ctx)

	var req containersv1alpha1.ComposeUpRequest
	err := r.Get(ctx, composeKeyForExtension(ext), &req)
	if apierrors.IsNotFound(err) {
		return r.createComposeUpRequest(ctx, ext, projectName)
	} else if err != nil {
		log.Error(err, "Failed to get ComposeUpRequest for Extension")
		return err
	}

	// The request already exists.
	settled := apimeta.FindStatusCondition(req.Status.Conditions, containersv1alpha1.ComposeUpRequestConditionSettled)
	if settled == nil || settled.Status != metav1.ConditionTrue {
		// The request already exists, but is not settled; we're still starting.
		return r.setCondition(ctx, ext,
			v1alpha1.ExtensionConditionStarted,
			metav1.ConditionFalse,
			v1alpha1.ExtensionStartedReasonStarting,
			"Extension is starting")
	}
	// The request has settled; see if it succeeded.
	if settled.Reason == containersv1alpha1.ComposeUpRequestSettledReasonSucceeded {
		return r.setCondition(ctx, ext,
			v1alpha1.ExtensionConditionStarted,
			metav1.ConditionTrue,
			v1alpha1.ExtensionStartedReasonStarted,
			"Extension has started")
	}
	return r.setCondition(ctx, ext,
		v1alpha1.ExtensionConditionStarted,
		metav1.ConditionFalse,
		v1alpha1.ExtensionStartedReasonStartFailed,
		fmt.Sprintf("Extension failed to start: %s", settled.Message))
}

// createComposeUpRequest creates a new ComposeUpRequest to start ext's
// containers.  This may be because the request failed and got reaped, in which
// case we are going to retry.
func (r *ExtensionReconciler) createComposeUpRequest(ctx context.Context, ext *v1alpha1.Extension, projectName string) error {
	log := logf.FromContext(ctx)

	extensionDir, err := extensionInstallDir(ext)
	if err != nil {
		log.Error(err, "Failed to get extension install directory")
		return err
	}
	composeKey := composeKeyForExtension(ext)
	req := containersv1alpha1.ComposeUpRequest{
		Name:      composeKey.Name,
		Namespace: composeKey.Namespace,
		Spec: containersv1alpha1.ComposeUpRequestSpec{
			Namespace:  getContainerNamespace(ext),
			Name:       projectName,
			WorkingDir: filepath.Join(extensionDir, "compose"),
			Env: map[string]string{
				"DESKTOP_PLUGIN_IMAGE": ext.Status.Image,
			},
		},
	}
	err = controllerutil.SetOwnerReference(
		ext,
		&req,
		r.Scheme(),
		controllerutil.WithBlockOwnerDeletion(true))
	if err != nil {
		return err
	}
	if err := r.Create(ctx, &req); err != nil {
		log.Error(err, "Failed to create ComposeUpRequest for Extension")
		return err
	}
	return r.setCondition(ctx, ext,
		v1alpha1.ExtensionConditionStarted,
		metav1.ConditionFalse,
		v1alpha1.ExtensionStartedReasonStarting,
		"Extension is starting")
}
