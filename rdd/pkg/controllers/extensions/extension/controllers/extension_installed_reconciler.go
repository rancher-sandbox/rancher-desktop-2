// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/distribution/reference"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/instance"
)

// installedFinalizer is added to Extension resources so uninstall can run
// the pre-uninstall script and delete extracted files before the resource is
// actually removed.
const installedFinalizer = "extensions.rancherdesktop.io/installed"

// imagePullRequestExtensionLabel labels ImagePullRequest objects created by
// this reconciler with the owning Extension's name, so an existing request
// can be found via a label selector instead of a deterministic name.
const imagePullRequestExtensionLabel = "extensions.rancherdesktop.io/extension"

// extensionNamespace is the container namespace extension images are
// pulled into; this matches rancher-sandbox/rancher-desktop.
const extensionNamespace = "rancher-desktop-extensions"

// deleteRetryDelay is how long to wait before retrying file deletion after
// it fails, to avoid a tight requeue loop.
const deleteRetryDelay = 30 * time.Second

// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=extensions.rancherdesktop.io,resources=extensions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.rancherdesktop.io,resources=apps,verbs=get;list;watch
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=imagepullrequests,verbs=get;list;watch;create;update;patch;delete

// ExtensionInstalledReconciler reconciles an Extension object's Installed
// condition: downloading and extracting the image, running its post-install
// script, and (on deletion) running the pre-uninstall script and cleaning up
// extracted files.
type ExtensionInstalledReconciler struct {
	client.Client
}

var _ reconcile.ObjectReconciler[*v1alpha1.Extension] = &ExtensionInstalledReconciler{}

// Reconcile implements the [reconcile.ObjectReconciler] interface for the ExtensionInstalledReconciler.
func (r *ExtensionInstalledReconciler) Reconcile(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Extension Installed condition",
		"name", ext.Name, "namespace", ext.Namespace)

	if base.IsBeingDeleted(ext) {
		return r.reconcileDelete(ctx, ext)
	}

	if !controllerutil.ContainsFinalizer(ext, installedFinalizer) {
		return ctrl.Result{}, r.addFinalizer(ctx, ext)
	}

	installed := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionInstalled)

	switch {
	case installed == nil, installed.Reason == v1alpha1.ExtensionInstalledReasonResolving:
		return ctrl.Result{}, r.resolveImage(ctx, ext)

	case installed.ObservedGeneration < ext.Generation:
		// spec.image has changed since the Installed condition was last
		// updated; restart the pipeline from the top. Uninstall is handled
		// separately (via reconcileDelete above) and never reaches here, so
		// this only applies to the install pipeline, which is always safe
		// to restart.
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonResolving,
			"Resolving extension image reference")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonDownloading:
		return r.download(ctx, ext)

	case installed.Reason == v1alpha1.ExtensionInstalledReasonExtracting:
		return ctrl.Result{}, r.extract(ctx, ext)

	case installed.Reason == v1alpha1.ExtensionInstalledReasonPostInstallRunning:
		// TODO: run the extension's post-install script (r.runPostInstall),
		// then set Installed or PostInstallFailed.
		return ctrl.Result{}, nil

	case installed.Reason == v1alpha1.ExtensionInstalledReasonResolveFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonDownloadFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonExtractFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonPostInstallFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonInstalled:
		// Terminal states (or Installed); the generation check above
		// already handles restarting on a spec change, so there is nothing
		// more to do here.
		return ctrl.Result{}, nil

	default:
		// Should never be reached: reconcileDelete handles Uninstalled (and
		// the other delete-related reasons) once the extension is being
		// deleted, and every other Installed reason is handled above.
		log.Error(nil, "Unexpected Installed condition reason", "reason", installed.Reason)
		return ctrl.Result{}, nil
	}
}

// resolveImage resolves ext.Spec.Image into status.image: if the image
// reference has no tag, it defaults to "latest" (real tag discovery, e.g.
// picking the highest semver tag, is not yet implemented). On success it
// sets status.image and advances the Installed condition to Downloading; on
// an invalid image reference it sets ResolveFailed (terminal).
func (r *ExtensionInstalledReconciler) resolveImage(ctx context.Context, ext *v1alpha1.Extension) error {
	named, err := reference.ParseNormalizedNamed(ext.Spec.Image)
	if err != nil {
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonResolveFailed,
			fmt.Sprintf("invalid image reference %q: %v", ext.Spec.Image, err))
	}
	resolved := reference.TagNameOnly(named).String()

	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status.Image = resolved
		apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ExtensionConditionInstalled,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: latest.Generation,
			Reason:             v1alpha1.ExtensionInstalledReasonDownloading,
			Message:            "Downloading extension image",
		})
		return r.Status().Update(ctx, latest)
	})
}

// download ensures an ImagePullRequest exists matching ext.Status.Image
// (creating one, labelled with imagePullRequestExtensionLabel, if none is
// found, or if the existing one is for a stale image reference), and
// advances the Installed condition based on its Complete/Failed conditions:
// Extracting on completion, DownloadFailed (terminal) on failure, or stays
// at Downloading (requeuing) while the pull is still in progress.
func (r *ExtensionInstalledReconciler) download(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	var pullRequests containersv1alpha1.ImagePullRequestList
	if err := r.List(ctx, &pullRequests,
		client.InNamespace(ext.Namespace),
		client.MatchingLabels{imagePullRequestExtensionLabel: ext.Name},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ImagePullRequests for extension %s: %w", ext.Name, err)
	}

	if len(pullRequests.Items) == 0 {
		return ctrl.Result{}, r.createImagePullRequest(ctx, ext)
	}

	index := slices.IndexFunc(pullRequests.Items, func(item containersv1alpha1.ImagePullRequest) bool {
		return item.Spec.RepoTag == ext.Status.Image
	})
	var pullRequest *containersv1alpha1.ImagePullRequest

	for i := range pullRequests.Items {
		// Delete any stale ImagePullRequests (there should only be one); ignore any
		// errors.
		if i == index {
			// This is the one we want to keep; skip deletion.
			pullRequest = &pullRequests.Items[i]
			continue
		}
		//nolint:gocritic // It doesn't know that client.IgnoreNotFound is a check.
		if err := r.Delete(ctx, &pullRequests.Items[i]); client.IgnoreNotFound(err) != nil {
			logf.FromContext(ctx).Error(err, "Failed to delete duplicate ImagePullRequest",
				"name", pullRequests.Items[i].Name)
		}
	}

	if pullRequest == nil {
		// We had lots of previous pull requests, none of them correct.
		return ctrl.Result{}, r.createImagePullRequest(ctx, ext)
	}

	if pullRequest.Spec.RepoTag != ext.Status.Image {
		// status.image changed (e.g. the user updated spec.image's tag)
		// since this ImagePullRequest was created; discard it and start a
		// new pull for the current image.
		if err := r.Delete(ctx, pullRequest); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(fmt.Errorf(
				"failed to delete stale ImagePullRequest %s for extension %s: %w",
				pullRequest.Name, ext.Name, err))
		}
		return ctrl.Result{}, r.createImagePullRequest(ctx, ext)
	}

	cond := apimeta.FindStatusCondition(pullRequest.Status.Conditions, "Settled")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		// Still downloading; wait for the ImagePullRequest's status to change.
		return ctrl.Result{}, nil
	}
	if cond.Reason == "Finished" {
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonExtracting,
			"Extracting extension image")
	}

	// Copying has failed
	message := "Failed to download extension image"
	if cond.Message != "" {
		message = cond.Message
	}
	return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
		metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonDownloadFailed, message)
}

// createImagePullRequest creates a new ImagePullRequest for ext.Status.Image,
// labelled with imagePullRequestExtensionLabel and owned by ext.
func (r *ExtensionInstalledReconciler) createImagePullRequest(ctx context.Context, ext *v1alpha1.Extension) error {
	generateName := ext.Name + "-pull-"
	if len(generateName) > 63 {
		// The name prefix is too long
		generateName = fmt.Sprintf("ext-%02x-pull-", sha1.Sum([]byte(ext.Name)))
	}
	pullRequest := &containersv1alpha1.ImagePullRequest{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generateName,
			Namespace:    ext.Namespace,
			Labels: map[string]string{
				imagePullRequestExtensionLabel: ext.Name,
			},
		},
		Spec: containersv1alpha1.ImagePullRequestSpec{
			Namespace: extensionNamespace,
			RepoTag:   ext.Status.Image,
		},
	}
	if err := ctrl.SetControllerReference(ext, pullRequest, r.Client.Scheme()); err != nil {
		return fmt.Errorf("failed to set owner reference on ImagePullRequest: %w", err)
	}
	if err := r.Create(ctx, pullRequest); err != nil {
		return fmt.Errorf("failed to create ImagePullRequest for extension %s: %w", ext.Name, err)
	}
	// The ImagePullRequest's status will trigger another reconcile once
	// the pull completes or fails (via Owns in SetupWithManager).
	return nil
}

// extract ensures that [ExtensionExtractedReconciler] is processing this the
// given extension, and handles when the Extracted condition is finished.
func (r *ExtensionInstalledReconciler) extract(ctx context.Context, ext *v1alpha1.Extension) error {
	condition := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionExtracted)
	switch {
	case condition == nil:
		// The extension has not been extracted yet; start the process
		key := client.ObjectKeyFromObject(ext)
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			latest := &v1alpha1.Extension{}
			if err := r.Get(ctx, key, latest); err != nil {
				return client.IgnoreNotFound(err)
			}
			changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               v1alpha1.ExtensionConditionExtracted,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: latest.Generation,
				Reason:             v1alpha1.ExtensionExtractedReasonPreparing,
				Message:            "Starting extraction",
			})
			if !changed {
				return nil
			}
			return r.Status().Update(ctx, latest)
		})
	case condition.Reason == v1alpha1.ExtensionExtractedReasonFailed:
		// Extraction failed; make sure the installed condition reflects this.
		return r.setInstalledCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionInstalledReasonExtractFailed, condition.Message)
	case condition.Reason == v1alpha1.ExtensionExtractedReasonCompleted:
		// Extraction completed successfully; proceed to the next step.
		return r.setInstalledCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionInstalledReasonExtracted, condition.Message)
	default:
		return nil
	}
}

// deleteFiles removes the extension's install directory (created by
// extract) from disk. Any pre-uninstall script is assumed to have already
// been run (or attempted) by the time this is called; this only needs to
// worry about the extracted files themselves.
func (r *ExtensionInstalledReconciler) deleteFiles(ext *v1alpha1.Extension) error {
	installDir, err := extensionInstallDir(ext)
	if err != nil {
		return fmt.Errorf("failed to determine extension install directory: %w", err)
	}
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("failed to delete extension files: %w", err)
	}

	return nil
}

// extensionInstallDir returns the directory extract copies an extension's
// files into (and that deleteFiles removes), unique to the extension.
func extensionInstallDir(ext *v1alpha1.Extension) (string, error) {
	// Encode the resolved image reference (status.image) as base64url so it
	// is safe to use as a single path component, matching rancher-desktop
	// 1's extension directory naming.
	image := ext.Status.Image
	if image == "" {
		return "", fmt.Errorf("extension %s has no resolved image reference", ext.Name)
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(image))
	return filepath.Join(instance.ExtensionDir(), encoded), nil
}

// reconcileDelete handles uninstall: it runs the pre-uninstall script and
// deletes extracted files (as tracked by the Installed condition's reason),
// then removes installedFinalizer once cleanup has finished.
func (r *ExtensionInstalledReconciler) reconcileDelete(ctx context.Context, ext *v1alpha1.Extension) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(ext, installedFinalizer) {
		return ctrl.Result{}, nil
	}

	installed := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionInstalled)

	switch {
	case installed == nil,
		installed.Reason == v1alpha1.ExtensionInstalledReasonResolving,
		installed.Reason == v1alpha1.ExtensionInstalledReasonResolveFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonDownloading,
		installed.Reason == v1alpha1.ExtensionInstalledReasonDownloadFailed:
		// Extraction never started (and, if installed == nil, status.image
		// was never even resolved), so there is nothing on disk at all
		// (not even a partial extraction to clean up); skip straight to
		// Uninstalled.
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonUninstalled, "Extension removed")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonExtracting,
		installed.Reason == v1alpha1.ExtensionInstalledReasonExtractFailed:
		// Extraction was attempted (and may have partially completed), so
		// there could be files on disk to clean up, but there is still no
		// pre-uninstall script to run since extraction never completed;
		// skip straight to Deleting.
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonDeleting, "Removing extension")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonPostInstallRunning,
		installed.Reason == v1alpha1.ExtensionInstalledReasonPostInstallFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonInstalled:
		// Extraction has completed, so a pre-uninstall script may exist on
		// disk to run; transition to PreUninstallRunning to do so.
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonPreUninstallRunning, "Removing extension")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonPreUninstallRunning:
		// TODO: this is currently stubbed out (it just advances straight to
		// Deleting); the pre-uninstall script should actually be run here
		// (ignoring failures, per the design doc).
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonDeleting, "Removing extension")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonDeleting:
		if err := r.deleteFiles(ext); err != nil {
			return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
				metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonDeleteFailed, err.Error())
		}
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonUninstalled, "Extension removed")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonDeleteFailed:
		// Unlike the install pipeline's failure states, DeleteFailed has no
		// spec change that could retrigger a retry (deletionTimestamp is
		// immutable) and no other way for a user to unstick it; unlike a
		// bad image reference, file-deletion errors have no permanent
		// cause, so retry rather than staying terminal, but wait at least
		// deleteRetryDelay since the last attempt to avoid a tight retry
		// loop: if not enough time has passed since LastTransitionTime,
		// just requeue for when it will have; once it has, transition back
		// to Deleting to actually retry (that status update happens to also
		// trigger an immediate reconcile via the watch, which is fine since
		// the delay has already elapsed by then).
		elapsed := time.Since(installed.LastTransitionTime.Time)
		if elapsed < deleteRetryDelay {
			return ctrl.Result{RequeueAfter: deleteRetryDelay - elapsed}, nil
		}
		return ctrl.Result{}, r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonDeleting, "Retrying extension removal")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonUninstalled:
		return ctrl.Result{}, r.removeFinalizer(ctx, ext)

	default:
		logf.FromContext(ctx).Error(nil, "Unexpected Installed condition reason while deleting", "reason", installed.Reason)
		return ctrl.Result{}, nil
	}
}

// addFinalizer adds installedFinalizer to ext, retrying on conflict against
// a freshly-fetched copy since other reconcilers may concurrently update it.
func (r *ExtensionInstalledReconciler) addFinalizer(ctx context.Context, ext *v1alpha1.Extension) error {
	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !controllerutil.AddFinalizer(latest, installedFinalizer) {
			return nil
		}
		return r.Update(ctx, latest)
	})
}

// removeFinalizer removes installedFinalizer from ext, retrying on conflict
// against a freshly-fetched copy.
func (r *ExtensionInstalledReconciler) removeFinalizer(ctx context.Context, ext *v1alpha1.Extension) error {
	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !controllerutil.RemoveFinalizer(latest, installedFinalizer) {
			return nil
		}
		return r.Update(ctx, latest)
	})
}

// setInstalledCondition sets the Installed condition on a freshly-fetched
// copy of ext, retrying on conflict.
func (r *ExtensionInstalledReconciler) setInstalledCondition(ctx context.Context, ext *v1alpha1.Extension, status metav1.ConditionStatus, reason, message string) error {
	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ExtensionConditionInstalled,
			Status:             status,
			ObservedGeneration: latest.Generation,
			Reason:             reason,
			Message:            message,
		})
		if !changed {
			return nil
		}
		return r.Status().Update(ctx, latest)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExtensionInstalledReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Extension{}).
		Owns(&containersv1alpha1.ImagePullRequest{}).
		Named("extension-installed-reconciler").
		Complete(reconcile.AsReconciler(mgr.GetClient(), r))
}
