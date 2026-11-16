// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"

	"github.com/distribution/reference"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/extensions/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
)

// installedFinalizer is added to Extension resources so uninstall can run
// the pre-uninstall script and delete extracted files before the resource is
// actually removed.
const installedFinalizer = "extensions.rancherdesktop.io/installed"

// imagePullRequestExtensionLabel labels ImagePullRequest objects created by
// this reconciler with the owning Extension's name, so an existing request
// can be found via a label selector instead of a deterministic name.
// Note that this is only an aid; the controller owner reference is still the
// authoritive source of ownership.
const imagePullRequestExtensionLabel = "extensions.rancherdesktop.io/extension"

// extensionNamespace is the container namespace extension images are
// pulled into; this matches rancher-sandbox/rancher-desktop.
const extensionNamespace = "rancher-desktop-extensions"

type scriptPhase string

const (
	scriptPhaseInstall   = "install"
	scriptPhaseUninstall = "uninstall"
	scriptPhaseShutdown  = "shutdown"
)

type scriptState struct {
	cmd       *exec.Cmd
	completed bool
	phase     scriptPhase
	err       error
}

// reconcileInstalled reconciles an Extension object's Installed condition.
// If this returns non-nil, the installed condition must be set.
func (r *ExtensionReconciler) reconcileInstalled(ctx context.Context, ext *v1alpha1.Extension) error {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling Extension Installed condition",
		"name", ext.Name, "namespace", ext.Namespace)

	if !controllerutil.ContainsFinalizer(ext, installedFinalizer) {
		if err := base.AddFinalizerWithRetry(ctx, r.Client, ext, installedFinalizer); err != nil {
			return err
		}
		return requeueError(0)
	}

	installed := apimeta.FindStatusCondition(ext.Status.Conditions, v1alpha1.ExtensionConditionInstalled)

	switch {
	case installed == nil:
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonResolving,
			"Resolving extension image reference")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonResolving:
		return r.resolveImage(ctx, ext)

	case installed.ObservedGeneration < ext.Generation:
		// spec.image has changed since the Installed condition was last
		// updated; restart the pipeline from the top. Uninstall is handled
		// separately (via reconcileDelete) and never reaches here, so
		// this only applies to the install pipeline, which is always safe
		// to restart.
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonResolving,
			"Resolving extension image reference")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonDownloading:
		return r.download(ctx, ext)

	case installed.Reason == v1alpha1.ExtensionInstalledReasonExtracting:
		return r.extract(ctx, ext)

	case installed.Reason == v1alpha1.ExtensionInstalledReasonExtracted:
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonPostInstallRunning,
			"Extension image extracted successfully")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonPostInstallRunning:
		pending, err := r.runScript(ext, scriptPhaseInstall)
		if err != nil {
			return r.setInstalledCondition(ctx, ext,
				metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonFailed,
				fmt.Sprintf("failed to run post-install script: %v", err))
		}
		if pending {
			return nil
		}
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionTrue, v1alpha1.ExtensionInstalledReasonInstalled,
			"Extension installed successfully")

	case installed.Reason == v1alpha1.ExtensionInstalledReasonFailed,
		installed.Reason == v1alpha1.ExtensionInstalledReasonInstalled,
		installed.Reason == v1alpha1.ExtensionInstalledReasonUninstalled:
		// Terminal states; the generation check above already handles restarting on
		// a spec change, so there is nothing more to do here.
		return nil

	default:
		// Should never be reached: reconcileDelete handles Uninstalled (and
		// the other delete-related reasons) once the extension is being
		// deleted, and every other Installed reason is handled above.
		return fmt.Errorf("unexpected Installed condition reason %q", installed.Reason)
	}
}

// resolveImage resolves ext.Spec.Image into status.image: if the image
// reference has no tag, it defaults to "latest" (real tag discovery, e.g.
// picking the highest semver tag, is not yet implemented). On success it
// sets status.image and advances the Installed condition to Downloading; on
// an invalid image reference it sets ResolveFailed (terminal).
func (r *ExtensionReconciler) resolveImage(ctx context.Context, ext *v1alpha1.Extension) error {
	named, err := reference.ParseNormalizedNamed(ext.Spec.Image)
	if err != nil {
		return r.setInstalledCondition(ctx, ext,
			metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonFailed,
			fmt.Sprintf("invalid image reference %q: %v", ext.Spec.Image, err))
	}
	// TODO: Pick the best tag.
	resolved := reference.TagNameOnly(named).String()

	key := client.ObjectKeyFromObject(ext)
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.Extension{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		latest.Status.Image = resolved
		changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:               v1alpha1.ExtensionConditionInstalled,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: latest.Generation,
			Reason:             v1alpha1.ExtensionInstalledReasonDownloading,
			Message:            "Downloading extension image",
		})
		if !changed {
			return nil
		}
		if err := r.Status().Update(ctx, latest); err != nil {
			return err
		}
		return requeueError(0)
	})
}

// download ensures an ImagePullRequest exists matching ext.Status.Image
// (creating one, labelled with imagePullRequestExtensionLabel, if none is
// found, or if the existing one is for a stale image reference), and
// advances the Installed condition based on its Complete/Failed conditions:
// Extracting on completion, DownloadFailed (terminal) on failure, or stays
// at Downloading (requeuing) while the pull is still in progress.
func (r *ExtensionReconciler) download(ctx context.Context, ext *v1alpha1.Extension) error {
	var list containersv1alpha1.ImageList
	err := r.List(ctx, &list,
		client.InNamespace(ext.Namespace),
		client.MatchingFields{".status.repoTag": ext.Status.Image})
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to list images for extension %s: %w", ext.Name, err)
	} else if err == nil && len(list.Items) > 0 {
		// The image already exists; update the labels, and the status.  We can't
		// use the status condition helpers here due to the labels change.
		image := &list.Items[0]
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest v1alpha1.Extension
			if err := r.Get(ctx, client.ObjectKeyFromObject(ext), &latest); err != nil {
				return client.IgnoreNotFound(err)
			}
			changed := apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:               v1alpha1.ExtensionConditionInstalled,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: latest.Generation,
				Reason:             v1alpha1.ExtensionInstalledReasonExtracting,
				Message:            "Extension image successfully downloaded",
			})
			if latest.Status.Labels == nil {
				latest.Status.Labels = make(map[string]string)
			}
			for k, v := range image.Status.Labels {
				if latest.Status.Labels[k] != v {
					latest.Status.Labels[k] = v
					changed = true
				}
			}
			if changed {
				if err := r.Status().Update(ctx, &latest); err != nil {
					return err
				}
				return requeueError(0)
			}
			return nil
		})
	}

	var pullRequests containersv1alpha1.ImagePullRequestList
	err = r.List(
		ctx, &pullRequests,
		client.InNamespace(ext.Namespace),
		client.MatchingLabels{imagePullRequestExtensionLabel: ext.Name},
	)
	if err != nil {
		return fmt.Errorf("failed to list ImagePullRequests for extension %s: %w", ext.Name, err)
	}

	pullRequests.Items = slices.DeleteFunc(pullRequests.Items, func(item containersv1alpha1.ImagePullRequest) bool {
		return !metav1.IsControlledBy(&item, ext)
	})

	if len(pullRequests.Items) == 0 {
		return r.createImagePullRequest(ctx, ext)
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
		return r.createImagePullRequest(ctx, ext)
	}

	if pullRequest.Spec.RepoTag != ext.Status.Image {
		// status.image changed (e.g. the user updated spec.image's tag)
		// since this ImagePullRequest was created; discard it and start a
		// new pull for the current image.
		err := r.Delete(ctx, pullRequest)
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf(
				"failed to delete stale ImagePullRequest %s for extension %s: %w",
				pullRequest.Name, ext.Name, err,
			)
		}
		return r.createImagePullRequest(ctx, ext)
	}

	cond := apimeta.FindStatusCondition(pullRequest.Status.Conditions,
		containersv1alpha1.ImagePullRequestConditionSettled)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		// Still downloading; wait for the ImagePullRequest's status to change.
		return requeueError(0)
	}
	if cond.Reason == "Finished" {
		// Unexpected state: the ImagePullRequest has finished, but the image is
		// missing.  Either the download finished part way through this function, or
		// the image was deleted after the download finished.  Either way, this can
		// be resolved by trying the download again.
		err := r.Delete(ctx, pullRequest)
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf(
				"failed to delete finished obsolete ImagePullRequest %s for extension %s: %w",
				pullRequest.Name, ext.Name, err,
			)
		}
		return r.createImagePullRequest(ctx, ext)
	}

	// Download failed
	message := "Failed to download extension image"
	if cond.Message != "" {
		message = fmt.Sprintf("%s: %s", message, cond.Message)
	}
	return r.setInstalledCondition(ctx, ext,
		metav1.ConditionFalse, v1alpha1.ExtensionInstalledReasonFailed, message)
}

// createImagePullRequest creates a new ImagePullRequest for ext.Status.Image,
// labelled with imagePullRequestExtensionLabel and owned by ext.
func (r *ExtensionReconciler) createImagePullRequest(ctx context.Context, ext *v1alpha1.Extension) error {
	generateName := ext.Name + "-pull-"
	if len(generateName) > 63 {
		// The name prefix is too long
		generateName = fmt.Sprintf("ext-%02x-pull-", sha1.Sum([]byte(ext.Name)))
	}
	pullRequest := &containersv1alpha1.ImagePullRequest{
		GenerateName: generateName,
		Namespace:    ext.Namespace,
		Labels: map[string]string{
			imagePullRequestExtensionLabel: ext.Name,
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
	return requeueError(0)
}

// extract ensures that the Extracted condition is processing this the
// given extension, and handles when the Extracted condition is finished.
func (r *ExtensionReconciler) extract(ctx context.Context, ext *v1alpha1.Extension) error {
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
			if err := r.Status().Update(ctx, latest); err != nil {
				return err
			}
			return requeueError(0)
		})
	case condition.Reason == v1alpha1.ExtensionExtractedReasonFailed:
		// Extraction failed; make sure the installed condition reflects this.
		return r.setInstalledCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionInstalledReasonFailed, fmt.Sprintf("extraction failed: %s", condition.Message))
	case condition.Reason == v1alpha1.ExtensionExtractedReasonCompleted && condition.Status == metav1.ConditionTrue:
		// Extraction completed successfully; proceed to the next step.
		return r.setInstalledCondition(ctx, ext, metav1.ConditionFalse,
			v1alpha1.ExtensionInstalledReasonExtracted, condition.Message)
	default:
		return nil
	}
}

// runScript manages running an install/uninstall script; it returns a boolean
// indicating the script is still in progress.
func (r *ExtensionReconciler) runScript(ext *v1alpha1.Extension, phase scriptPhase) (bool, error) {
	existing, ok := r.scripts.Load(ext.GetUID())
	if ok {
		if existing.phase != phase {
			// Phase has changed; abort the process.
			err := existing.cmd.Process.Kill()
			r.scripts.Delete(ext.GetUID())
			if err != nil && !errors.Is(err, os.ErrProcessDone) {
				return false, err
			}
			// Falls through, where we create the new process as new.
		} else if existing.completed {
			return false, existing.err
		} else {
			// Still pending
			return true, nil
		}
	}
	// If we get here, there's no old state, or it has been aborted.
	manifest, err := getManifest(ext)
	if err != nil {
		return false, err
	}
	if manifest == nil {
		return false, nil // No manifest, therefore no script to uninstall
	}
	var scriptArgs []string
	switch phase {
	case scriptPhaseInstall:
		scriptArgs = manifest.Host.InstallScript.Get()
	case scriptPhaseUninstall:
		scriptArgs = manifest.Host.UninstallScript.Get()
	case scriptPhaseShutdown:
		scriptArgs = manifest.Host.ShutdownScript.Get()
	default:
		panic(fmt.Sprintf("unknown script phase: %s", phase))
	}
	if len(scriptArgs) < 1 {
		return false, nil // No script to run
	}
	// The executable path is relative to the install root; find where we
	// extracted it.
	dir, err := extensionInstallDir(ext)
	if err != nil {
		return false, err
	}
	executable := filepath.Join(dir, "bin", path.Base(scriptArgs[0]))
	cmd := exec.CommandContext(r.ctx, executable, scriptArgs[1:]...)
	if cmd.Dir, err = extensionInstallDir(ext); err != nil {
		return false, err
	}
	if err = cmd.Start(); err != nil {
		return false, err
	}
	r.scripts.Store(ext.GetUID(), scriptState{
		phase: phase,
		cmd:   cmd,
	})
	go func() {
		err := cmd.Wait()
		r.scripts.Modify(ext.GetUID(), func(value scriptState, exists bool) (scriptState, bool) {
			if value.cmd != cmd {
				return value, exists
			}
			value.completed = true
			value.err = err
			return value, true
		})
		select {
		case r.requeueCh <- event.TypedGenericEvent[*v1alpha1.Extension]{Object: ext}:
		case <-r.ctx.Done():
		}
	}()
	return true, nil
}

// setInstalledCondition sets the Installed condition on a freshly-fetched
// copy of ext, retrying on conflict.
func (r *ExtensionReconciler) setInstalledCondition(ctx context.Context, ext *v1alpha1.Extension, status metav1.ConditionStatus, reason, message string) error {
	return r.setCondition(ctx, ext, v1alpha1.ExtensionConditionInstalled, status, reason, message)
}
