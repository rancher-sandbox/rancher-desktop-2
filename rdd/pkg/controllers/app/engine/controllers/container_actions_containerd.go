// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"syscall"
	"time"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

// processContainerAction handles a container carrying the AnnotationAction
// annotation. It dispatches the containerd call, records the outcome in
// status.lastAction, and then removes the annotation.
//
// The containerd call runs before the status and metadata patches so that a
// crash mid-flight leaves the annotation in place and the next reconcile
// replays the action. Start, stop, pause, and unpause are idempotent
// against a container already in the target state, so replay is safe.
// Restart has no target state to match: a replay recreates the task a second
// time, which the controller cannot distinguish from a deliberate re-request.
func (w *containerdWatcher) processContainerAction(ctx context.Context, c *containersv1alpha1.Container) error {
	raw, ok := c.Annotations[containersv1alpha1.AnnotationAction]
	if !ok {
		return nil
	}

	log := logf.FromContext(ctx).WithName("containerd-watcher")
	action := containersv1alpha1.ContainerAction(raw)
	observedAt := metav1.Now()

	// The webhook rejects invalid action values, but one written while the
	// webhook is offline can still reach storage. Drop such values here;
	// otherwise the CRD enum rejects the status.lastAction write, the
	// annotation stays in place, and every reconcile retries forever.
	if !action.IsValid() {
		log.Info("Dropping invalid container action annotation", "id", c.Name, "action", raw)
		return w.removeActionAnnotation(ctx, c, raw)
	}

	dispatchErr := w.dispatchContainerAction(ctx, log, c, action)

	lastAction := &containersv1alpha1.ContainerLastAction{
		Action:      action,
		ObservedAt:  observedAt,
		CompletedAt: metav1.Now(),
	}
	if dispatchErr == nil {
		lastAction.State = containersv1alpha1.ContainerActionSucceeded
	} else {
		lastAction.State = containersv1alpha1.ContainerActionFailed
		lastAction.Error = dispatchErr.Error()
		log.Info("Container action failed", "id", c.Name, "action", action, "error", dispatchErr)
	}

	latest, err := w.patchContainerLastAction(ctx, c.Name, lastAction)
	if err != nil {
		return fmt.Errorf("failed to patch lastAction for %s: %w", c.Name, err)
	}
	if latest == nil {
		// Mirror was deleted between dispatch and the status patch; nothing
		// left to clean up.
		return nil
	}
	if err := w.removeActionAnnotation(ctx, latest, raw); err != nil {
		return fmt.Errorf("failed to remove action annotation for %s: %w", c.Name, err)
	}
	return nil
}

// dispatchContainerAction executes the containerd call for a single action.
// The caller pre-validates the action name; the default branch triggers only
// when a new ContainerAction value is added to the type but not to the switch.
func (w *containerdWatcher) dispatchContainerAction(ctx context.Context, log logr.Logger, c *containersv1alpha1.Container, action containersv1alpha1.ContainerAction) error {
	ns := c.Status.Namespace
	if ns == "" {
		// A mirror without a namespace has no engine object to act on.
		return errors.New("container mirror has no namespace")
	}

	ctr, err := w.resolveContainer(ctx, ns, c.Name)
	if err != nil {
		return err
	}
	nsCtx := namespaces.WithNamespace(ctx, ns)

	switch action {
	case containersv1alpha1.ContainerActionStart:
		log.Info("Starting container", "id", c.Name)
		return w.startContainer(nsCtx, log, ctr)
	case containersv1alpha1.ContainerActionStop:
		log.Info("Stopping container", "id", c.Name)
		return w.stopTask(nsCtx, log, ctr)
	case containersv1alpha1.ContainerActionPause:
		log.Info("Pausing container", "id", c.Name)
		return w.pauseContainer(nsCtx, ctr)
	case containersv1alpha1.ContainerActionUnpause:
		log.Info("Unpausing container", "id", c.Name)
		return w.unpauseContainer(nsCtx, ctr)
	case containersv1alpha1.ContainerActionRestart:
		log.Info("Restarting container", "id", c.Name)
		return w.restartContainer(nsCtx, log, ctr)
	}
	return fmt.Errorf("unknown container action %q", action)
}

// resolveContainer maps a Container mirror name back to its containerd
// container. The mirror name normally IS the container ID, so LoadContainer
// hits directly. IDs that are not valid K8s names get hashed mirror names
// (see containerdMirrorName), which only a scan of the namespace can map back.
func (w *containerdWatcher) resolveContainer(ctx context.Context, ns, mirrorName string) (containerdclient.Container, error) {
	nsCtx := namespaces.WithNamespace(ctx, ns)
	ctr, err := w.cli.LoadContainer(nsCtx, mirrorName)
	if err == nil {
		return ctr, nil
	}
	if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to load container %s: %w", mirrorName, err)
	}

	ctrs, listErr := w.cli.Containers(nsCtx)
	if listErr != nil {
		return nil, fmt.Errorf("failed to list containers in namespace %s: %w", ns, listErr)
	}
	for _, candidate := range ctrs {
		if containerdMirrorName(ns, candidate.ID()) == mirrorName {
			return candidate, nil
		}
	}
	return nil, err
}

// startContainer starts the container's task, matching Docker's no-op
// semantics: a start on an already-running container returns nil.
func (w *containerdWatcher) startContainer(nsCtx context.Context, log logr.Logger, ctr containerdclient.Container) error {
	task, err := ctr.Task(nsCtx, nil)
	if errdefs.IsNotFound(err) {
		return w.startTask(nsCtx, log, ctr)
	}
	if err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	st, err := task.Status(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get task status: %w", err)
	}
	switch st.Status {
	case containerdclient.Running, containerdclient.Paused, containerdclient.Pausing:
		// Already running: Docker returns 304 Not Modified for this case.
		return nil
	case containerdclient.Created:
		return task.Start(nsCtx)
	case containerdclient.Stopped:
		if _, err := task.Delete(nsCtx); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to delete stopped task: %w", err)
		}
		return w.startTask(nsCtx, log, ctr)
	}
	return fmt.Errorf("cannot start container in state %q", st.Status)
}

// startTask creates and starts a fresh task for the container. Shared by
// start and restart.
//
// nerdctl records its log driver as a binary:// URI in the nerdctl/log-uri
// label; recreating the task with it keeps `nerdctl logs` working, and NullIO
// covers containers created without it (e.g. via ctr).
func (w *containerdWatcher) startTask(nsCtx context.Context, log logr.Logger, ctr containerdclient.Container) error {
	creator := cio.NullIO
	info, err := ctr.Info(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get container info: %w", err)
	}
	if raw := info.Labels["nerdctl/log-uri"]; raw != "" {
		if u, err := url.Parse(raw); err == nil {
			creator = cio.LogURI(u)
		}
	}

	task, err := ctr.NewTask(nsCtx, creator)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}
	if err := task.Start(nsCtx); err != nil {
		// Best-effort cleanup so a half-created task cannot block the next
		// attempt.
		if _, delErr := task.Delete(nsCtx, containerdclient.WithProcessKill); delErr != nil {
			log.Error(delErr, "Failed to clean up half-created task", "id", ctr.ID())
		}
		return fmt.Errorf("failed to start task: %w", err)
	}
	return nil
}

// stopTask sends SIGTERM to the container's task and waits Docker's default
// 10-second grace period before escalating to SIGKILL. Shared by stop and
// restart. The stopped task is left in place: nerdctl derives the Exited
// state from it.
func (w *containerdWatcher) stopTask(nsCtx context.Context, _ logr.Logger, ctr containerdclient.Container) error {
	task, err := ctr.Task(nsCtx, nil)
	if errdefs.IsNotFound(err) {
		// No task: nothing to stop, matching Docker's no-op semantics.
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	st, err := task.Status(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get task status: %w", err)
	}
	switch st.Status {
	case containerdclient.Stopped, containerdclient.Created:
		// Stopped already, or created but never started: nothing to stop.
		return nil
	}

	// Wait must be armed before Kill so the exit is observed.
	exitCh, err := task.Wait(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to wait for task: %w", err)
	}
	if err := task.Kill(nsCtx, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to signal task: %w", err)
	}
	select {
	case <-exitCh:
		return nil
	case <-time.After(10 * time.Second):
	}
	if err := task.Kill(nsCtx, syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to kill task: %w", err)
	}
	select {
	case <-exitCh:
		return nil
	case <-nsCtx.Done():
		return nsCtx.Err()
	}
}

// pauseContainer pauses the container's task. Pausing an already-paused
// container returns nil: two reconcile ticks can read the same action
// annotation through the informer cache before the removal lands, and the
// pre-check keeps the second dispatch from flipping lastAction to Failed.
func (w *containerdWatcher) pauseContainer(nsCtx context.Context, ctr containerdclient.Container) error {
	task, err := ctr.Task(nsCtx, nil)
	if errdefs.IsNotFound(err) {
		return errors.New("cannot pause container: not running")
	}
	if err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	st, err := task.Status(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get task status: %w", err)
	}
	switch st.Status {
	case containerdclient.Paused:
		return nil
	case containerdclient.Running:
		return task.Pause(nsCtx)
	}
	return errors.New("cannot pause container: not running")
}

// unpauseContainer resumes the container's task. Resuming an already-running
// container returns nil, for the same informer-cache reason as pauseContainer.
func (w *containerdWatcher) unpauseContainer(nsCtx context.Context, ctr containerdclient.Container) error {
	task, err := ctr.Task(nsCtx, nil)
	if errdefs.IsNotFound(err) {
		return errors.New("cannot unpause container: not running")
	}
	if err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	st, err := task.Status(nsCtx)
	if err != nil {
		return fmt.Errorf("failed to get task status: %w", err)
	}
	switch st.Status {
	case containerdclient.Paused:
		return task.Resume(nsCtx)
	case containerdclient.Running:
		return nil
	}
	return errors.New("cannot unpause container: not running")
}

// restartContainer stops and recreates the container's task. Unlike Docker's
// restart, a task cannot be restarted in place — it is recreated; this also
// makes restart start a stopped container, matching Docker.
func (w *containerdWatcher) restartContainer(nsCtx context.Context, log logr.Logger, ctr containerdclient.Container) error {
	if err := w.stopTask(nsCtx, log, ctr); err != nil {
		return err
	}
	if task, err := ctr.Task(nsCtx, nil); err == nil {
		if _, err := task.Delete(nsCtx); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to delete task: %w", err)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to load task: %w", err)
	}
	return w.startTask(nsCtx, log, ctr)
}

// deleteContainer removes a container from containerd when its mirror is
// deleted. NotFound is treated as success; other errors propagate so the
// caller keeps the finalizer and retries.
//
// A K8s delete of the mirror means "remove this container", so a running task
// is killed first (Force semantics matching the moby path). containerd has no
// in-use protection, so there is no conflict-until-container-removed behavior.
func (w *containerdWatcher) deleteContainer(ctx context.Context, c *containersv1alpha1.Container) error {
	ns := c.Status.Namespace
	if ns == "" {
		// A bare user-created mirror carries no engine reference — parallel
		// to processImageFinalizers' empty-status guard.
		return nil
	}

	ctr, err := w.resolveContainer(ctx, ns, c.Name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	nsCtx := namespaces.WithNamespace(ctx, ns)

	if task, err := ctr.Task(nsCtx, nil); err == nil {
		if _, err := task.Delete(nsCtx, containerdclient.WithProcessKill); err != nil && !errdefs.IsNotFound(err) {
			return fmt.Errorf("failed to delete task for %s: %w", c.Name, err)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to load task for %s: %w", c.Name, err)
	}

	if err := ctr.Delete(nsCtx, containerdclient.WithSnapshotCleanup); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("failed to delete container %s: %w", c.Name, err)
	}
	return nil
}

// deleteImage removes an image record from containerd when its mirror is
// deleted. The delete is synchronous so the record is gone (GC complete) when
// the finalizer is stripped. NotFound is treated as success.
//
// containerd has no in-use protection: deleting a record referenced by a
// running container succeeds and the container keeps its snapshot. There is
// no Docker-style conflict-until-container-removed behavior.
func (w *containerdWatcher) deleteImage(ctx context.Context, img *containersv1alpha1.Image) error {
	ns := img.Status.Namespace
	if ns == "" {
		return nil
	}
	// Dangling records are named by their digest.
	ref := img.Status.RepoTag
	if ref == "" {
		ref = img.Status.ID
	}
	nsCtx := namespaces.WithNamespace(ctx, ns)
	err := w.cli.ImageService().Delete(nsCtx, ref, images.SynchronousDelete())
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete image %s: %w", ref, err)
	}
	return nil
}
