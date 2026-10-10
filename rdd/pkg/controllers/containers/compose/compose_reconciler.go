// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/controllers/base"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/api"
)

// The standard `docker compose` labels set on containers (and other
// resources) that belong to a compose project.
const (
	composeProjectLabel     = "com.docker.compose.project"
	composeConfigHashLabel  = "com.docker.compose.config-hash"
	composeWorkingDirLabel  = "com.docker.compose.project.working_dir"
	composeConfigFilesLabel = "com.docker.compose.project.config_files"
)

// containersIndexKey is the field index key used to look up containers by their
// name in the status of a ComposeProject.
const containersIndexKey = ".status.containers[*].name"

// composeRequest is the typed reconcile request for reconciler. It carries
// the originating kind in addition to the namespaced name and UID.
type composeRequest struct {
	Kind string
	types.NamespacedName
	types.UID
}

// reconciler implements the ComposeProject reconcile loop.
type reconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=composeprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups=containers.rancherdesktop.io,resources=containers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile dispatches requests by source kind.
func (r *reconciler) Reconcile(ctx context.Context, req composeRequest) (ctrl.Result, error) {
	switch req.Kind {
	case v1alpha1.ComposeProjectKind:
		return ctrl.Result{}, nil
	case v1alpha1.ContainerKind:
		return r.reconcileContainer(ctx, req)
	default:
		logf.FromContext(ctx).Error(
			errors.New("unsupported reconcile request kind"),
			"Ignoring reconcile request",
			"kind", req.Kind,
			"name", req.Name,
			"namespace", req.Namespace,
		)
		return ctrl.Result{}, nil
	}
}

// reconcileContainer creates or updates a ComposeProject object, based on the
// container that triggered the reconcile request.
func (r *reconciler) reconcileContainer(ctx context.Context, req composeRequest) (ctrl.Result, error) {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Find the object (Container) that triggered the reconcile request.
		var container v1alpha1.Container
		if err := r.Get(ctx, req.NamespacedName, &container); err != nil {
			if apierrors.IsNotFound(err) {
				// The container was deleted, so remove its membership from any
				// Compose in the same namespace.
				return r.removeComposeMembership(ctx, req, nil)
			}
			return err
		}
		projectName := container.Status.Labels[composeProjectLabel]
		if projectName == "" {
			return r.removeComposeMembership(ctx, req, nil)
		}
		if container.Status.Labels[composeConfigHashLabel] == "" {
			// The resource is not part of a compose project, so remove its membership
			// from any Compose in the same namespace.
			return r.removeComposeMembership(ctx, req, nil)
		}

		var changed bool
		project := &v1alpha1.ComposeProject{
			Name: api.MirrorName[*v1alpha1.ComposeProject](
				fmt.Sprintf("%s.%s", container.Status.Namespace, projectName)),
			Namespace: container.GetNamespace(),
		}

		// Create the ComposeProject if it doesn't exist yet.  Optimistically try to
		// get it first; if that fails, try to create it, but that may have been
		// concurrently created instead.
		err := r.Get(ctx, client.ObjectKeyFromObject(project), project)
		if apierrors.IsNotFound(err) {
			controllerutil.AddFinalizer(project, mirrorFinalizer)
			if err = r.Create(ctx, project); apierrors.IsAlreadyExists(err) {
				err = r.Get(ctx, client.ObjectKeyFromObject(project), project)
			}
		}
		if err != nil {
			return fmt.Errorf(
				"failed to create compose project for container %s: %w", container.GetName(), err)
		}

		// Remove the container from any ComposeProject it may currently be a member
		// of, except for the one it is currently associated with.
		if err := r.removeComposeMembership(ctx, req, project); err != nil {
			return err
		}

		containerID := container.GetName()
		changed = changed || project.Status.Namespace != container.Status.Namespace
		project.Status.Namespace = container.Status.Namespace
		changed = changed || project.Status.Name != projectName
		project.Status.Name = projectName
		if container.Status.Labels[composeWorkingDirLabel] != "" {
			changed = changed || project.Status.WorkingDir != container.Status.Labels[composeWorkingDirLabel]
			project.Status.WorkingDir = container.Status.Labels[composeWorkingDirLabel]
		}
		configs := composeConfigFiles(container.Status.Labels[composeWorkingDirLabel], container.Status.Labels[composeConfigFilesLabel])
		if len(configs) > 0 {
			changed = changed || !slices.Equal(project.Status.Configs, configs)
			project.Status.Configs = configs
		}
		index := slices.IndexFunc(project.Status.Containers, func(m v1alpha1.ComposeProjectContainer) bool {
			return m.Name == containerID
		})
		if index >= 0 {
			changed = changed || project.Status.Containers[index].UID != container.GetUID()
			project.Status.Containers[index].UID = container.GetUID()
		} else {
			changed = true
			project.Status.Containers = append(project.Status.Containers, v1alpha1.ComposeProjectContainer{
				Name: containerID,
				UID:  container.GetUID(),
			})
		}
		if changed {
			apimeta.SetStatusCondition(&project.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.ComposeProjectConditionHasMembers,
				Status:  metav1.ConditionTrue,
				Reason:  v1alpha1.ComposeHasMembersReasonFound,
				Message: fmt.Sprintf("last found member: %s %s", req.Kind, container.GetName()),
			})
			return r.Status().Update(ctx, project)
		}
		return nil
	})
	return ctrl.Result{}, err
}

// removeComposeMembership removes the object referred to in the given request
// from the `HasMembers` status of any ComposeProject in the same namespace.
// If `project` is given, then membership will not be removed for that project.
func (r *reconciler) removeComposeMembership(ctx context.Context, req composeRequest, project *v1alpha1.ComposeProject) error {
	var list v1alpha1.ComposeProjectList
	err := r.List(ctx, &list, client.InNamespace(req.Namespace), client.MatchingFields{containersIndexKey: req.Name})
	if err != nil {
		return fmt.Errorf("failed to list compose projects with container id %q: %w", req.Name, err)
	}

	var errs []error
	for _, item := range list.Items {
		if project != nil && item.GetUID() == project.GetUID() {
			continue
		}
		itemKey := client.ObjectKeyFromObject(&item)
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var latest v1alpha1.ComposeProject
			if err := r.Get(ctx, itemKey, &latest); err != nil {
				return client.IgnoreNotFound(err)
			}
			i := slices.IndexFunc(latest.Status.Containers, func(c v1alpha1.ComposeProjectContainer) bool {
				if req.UID != "" && c.UID != req.UID {
					return false
				}
				return c.Name == req.Name
			})
			if i < 0 {
				return nil
			}
			latest.Status.Containers = slices.Delete(latest.Status.Containers, i, i+1)
			apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
				Type:    v1alpha1.ComposeProjectConditionHasMembers,
				Status:  metav1.ConditionUnknown,
				Reason:  v1alpha1.ComposeHasMembersReasonCalculating,
				Message: fmt.Sprintf("removing member: %s %s", req.Kind, req.Name),
			})
			return r.Status().Update(ctx, &latest)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to remove %q from %q: %w", req.Name, itemKey, err))
		}
	}

	return errors.Join(errs...)
}

// composeConfigFiles parses the comma-separated list of absolute compose
// file paths from the composeConfigFilesLabel value, and returns them
// relative to workingDir, matching ComposeStatus.Configs's documented
// convention. Paths that cannot be made relative to workingDir (or when
// workingDir is unknown) are returned unchanged.
func composeConfigFiles(workingDir, rawConfigFiles string) []string {
	if rawConfigFiles == "" {
		return nil
	}

	parentDirPrefix := fmt.Sprintf("..%c", filepath.Separator)
	paths := strings.Split(rawConfigFiles, ",")
	configs := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if workingDir == "" {
			configs = append(configs, path)
			continue
		}
		rel, err := filepath.Rel(workingDir, path)
		if err != nil || strings.HasPrefix(rel, parentDirPrefix) || strings.TrimLeft(rel, ".") == "" {
			configs = append(configs, path)
			continue
		}
		configs = append(configs, rel)
	}

	return configs
}

// SetupWithManager sets up the controller with the Manager.
func (r *reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := base.IndexCRDFields(ctx, &v1alpha1.ComposeProject{}, mgr); err != nil {
		return err
	}
	// status.containers is an array of ComposeProjectContainer, which cannot be
	// indexed via the CRD directly.  Instead, set up a client-side index so we
	// can find the project given a container.  This index will be used to find
	// the correct project to remove a container from when that container is
	// deleted.
	if err := base.IndexField(ctx, &v1alpha1.ComposeProject{}, mgr, containersIndexKey); err != nil {
		return err
	}

	hasLabelFunc := func(obj client.Object) bool {
		container, ok := obj.(*v1alpha1.Container)
		return ok && container.Status.Labels[composeProjectLabel] != ""
	}

	return builder.TypedControllerManagedBy[composeRequest](mgr).
		Named("compose-reconciler").
		WithLogConstructor(func(req *composeRequest) logr.Logger {
			log := mgr.GetLogger().WithValues("controller", "compose-reconciler")
			if req != nil {
				log = log.WithValues(
					"controllerKind", req.Kind,
					req.Kind, klog.KRef(req.Namespace, req.Name),
					"namespace", req.Namespace,
					"name", req.Name)
			}
			return log
		}).
		Watches(&v1alpha1.Container{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []composeRequest {
			return []composeRequest{{
				Kind:           v1alpha1.ContainerKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
				UID:            obj.GetUID(),
			}}
		}), builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(obj event.TypedCreateEvent[client.Object]) bool {
				return hasLabelFunc(obj.Object)
			},
			UpdateFunc: func(obj event.TypedUpdateEvent[client.Object]) bool {
				return hasLabelFunc(obj.ObjectNew) || hasLabelFunc(obj.ObjectOld)
			},
			DeleteFunc: func(obj event.TypedDeleteEvent[client.Object]) bool {
				return hasLabelFunc(obj.Object)
			},
			GenericFunc: func(obj event.TypedGenericEvent[client.Object]) bool {
				return hasLabelFunc(obj.Object)
			},
		})).
		Watches(&v1alpha1.ComposeProject{}, handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []composeRequest {
			return []composeRequest{{
				Kind:           v1alpha1.ComposeProjectKind,
				NamespacedName: client.ObjectKeyFromObject(obj),
				UID:            obj.GetUID(),
			}}
		})).
		Complete(r)
}
