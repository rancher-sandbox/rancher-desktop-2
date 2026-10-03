// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package compose

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	"gotest.tools/v3/assert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/api"
)

// spyingClient wraps a client.Client to count Create/Update/status-Update
// calls, so tests can assert on idempotency (i.e. that a reconcile that
// changes nothing does not attempt a redundant write) and on whether a
// Compose was created. Writes show up as plain Create/Update calls plus
// Status().Update() calls.
type spyingClient struct {
	client.Client
	createCalls       int
	updateCalls       int
	statusUpdateCalls int
}

func (c *spyingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCalls++
	if obj.GetUID() == "" {
		// The fake client (unlike a real API server) does not assign a UID on
		// creation; do so here so that code relying on GetUID() to detect
		// "found" vs "not found" objects behaves the same way it would
		// against a real cluster.
		obj.SetUID(uuid.NewUUID())
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *spyingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updateCalls++
	return c.Client.Update(ctx, obj, opts...)
}

func (c *spyingClient) Status() client.SubResourceWriter {
	return &spyingStatusWriter{SubResourceWriter: c.Client.Status(), spy: c}
}

// spyingStatusWriter counts Status().Update() calls on the wrapping
// spyingClient, since client.Client.Status() is otherwise unwrapped by
// embedding.
type spyingStatusWriter struct {
	client.SubResourceWriter
	spy *spyingClient
}

func (w *spyingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.spy.statusUpdateCalls++
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// writes returns the total number of write calls (Create + Update +
// Status().Update()) observed so far.
func (c *spyingClient) writes() int {
	return c.createCalls + c.updateCalls + c.statusUpdateCalls
}

// membersIndexFunc builds a client.IndexerFunc for the ".status.containers[*].name"
// client-side index, mirroring what is set up in SetupWithManager.
func membersIndexFunc(obj client.Object) []string {
	compose, ok := obj.(*v1alpha1.ComposeProject)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(compose.Status.Containers))
	for _, member := range compose.Status.Containers {
		out = append(out, member.Name)
	}
	return out
}

// newReconciler builds a reconciler backed by a fake client seeded with objs.
func newReconciler(t *testing.T, objs ...client.Object) (*reconciler, *spyingClient) {
	t.Helper()

	scheme := k8sruntime.NewScheme()
	assert.NilError(t, v1alpha1.AddToScheme(scheme))

	builder := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ComposeProject{}).
		WithObjects(objs...).
		WithIndex(&v1alpha1.ComposeProject{}, containersIndexKey, membersIndexFunc)

	fakeClient := &spyingClient{Client: builder.Build()}

	return &reconciler{Client: fakeClient}, fakeClient
}

// newContainer builds a Container mirror with the given k8s namespace/name,
// docker-context namespace (status.namespace), and docker labels. The UID is
// derived from name, so it's stable and unique per test resource.
func newContainer(k8sNamespace, name, dockerNamespace string, labels map[string]string) *v1alpha1.Container {
	return &v1alpha1.Container{
		Name:      name,
		Namespace: k8sNamespace,
		UID:       types.UID(name),
		Status: v1alpha1.ContainerStatus{
			Namespace: dockerNamespace,
			Labels:    labels,
		},
	}
}

// reconcileKind runs the reconciler for the given kind/key/uid, mirroring how
// a watch event would enqueue a composeRequest.
func reconcileKind(r *reconciler, kind string, key types.NamespacedName, uid types.UID) (ctrl.Result, error) {
	return r.Reconcile(context.Background(), composeRequest{
		Kind:           kind,
		NamespacedName: key,
		UID:            uid,
	})
}

// testReconcileResource runs the battery of subtests exercising
// reconcileContainer, keyed off the com.docker.compose.project* labels.
func TestReconcileContainer(t *testing.T) {
	t.Parallel()

	const k8sNamespace = "rancher-desktop"
	const dockerNamespace = "moby"

	keyFromProjectName := func(projectName string) client.ObjectKey {
		return types.NamespacedName{
			Name:      api.MirrorName[*v1alpha1.ComposeProject](fmt.Sprintf("%s.%s", dockerNamespace, projectName)),
			Namespace: k8sNamespace,
		}
	}

	t.Run("ignores container without the compose project label", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{"unrelated": "label"})
		r, fakeClient := newReconciler(t, resource)

		result, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.DeepEqual(t, result, ctrl.Result{})
		assert.Equal(t, fakeClient.writes(), 0)
	})

	t.Run("ignores container with no labels at all", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{})
		r, fakeClient := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.writes(), 0)
	})

	t.Run("returns no error for container that no longer exists", func(t *testing.T) {
		t.Parallel()
		r, fakeClient := newReconciler(t)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, types.NamespacedName{Namespace: k8sNamespace, Name: "gone"}, "gone-uid")
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.writes(), 0)
	})

	t.Run("ignores container with the compose project label but no config-hash label", func(t *testing.T) {
		// A resource must carry the config-hash label to be considered a real
		// compose-managed resource; `docker compose down` itself only ever
		// discovers/removes containers that have this label, so anything
		// missing it cannot actually be reconciled/torn down as a compose
		// project member.
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{
			composeProjectLabel: "myproject",
		})
		r, fakeClient := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.writes(), 0)

		var compose v1alpha1.ComposeProject
		err = r.Get(t.Context(), keyFromProjectName("myproject"), &compose)
		assert.Assert(t, apierrors.IsNotFound(err), "expected no ComposeProject to be created, got err=%v", err)
	})

	t.Run("creates a Compose with just the identity when only the project and config-hash labels are set", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{
			composeProjectLabel:    "myproject",
			composeConfigHashLabel: "somehash",
		})
		r, fakeClient := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.createCalls, 1)
		assert.Equal(t, fakeClient.statusUpdateCalls, 1)

		var compose v1alpha1.ComposeProject
		err = r.Get(t.Context(), keyFromProjectName("myproject"), &compose)
		assert.NilError(t, err)
		assert.Equal(t, compose.Status.Namespace, dockerNamespace)
		assert.Equal(t, compose.Status.Name, "myproject")
		assert.Equal(t, compose.Status.WorkingDir, "")
		assert.Equal(t, len(compose.Status.Configs), 0)
		assert.DeepEqual(t, compose.Status.Containers, []v1alpha1.ComposeProjectContainer{
			{Name: resource.GetName(), UID: resource.GetUID()},
		})
	})

	t.Run("populates workingDir and configs, relative to workingDir, when the labels are present", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{
			composeProjectLabel:     "myproject",
			composeConfigHashLabel:  "somehash",
			composeWorkingDirLabel:  "/home/user/myproject",
			composeConfigFilesLabel: "/home/user/myproject/compose.yaml,/home/user/myproject/compose.override.yaml,/etc/other/compose.yaml",
		})
		r, _ := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)

		var compose v1alpha1.ComposeProject
		assert.NilError(t, r.Get(t.Context(), keyFromProjectName("myproject"), &compose))
		assert.Equal(t, compose.Status.WorkingDir, "/home/user/myproject")
		// The first two files are under workingDir, so they become relative;
		// the third is outside workingDir, so it is kept as an absolute path.
		assert.DeepEqual(t, compose.Status.Configs, []string{
			"compose.yaml",
			"compose.override.yaml",
			"/etc/other/compose.yaml",
		})
	})

	t.Run("is idempotent: reconciling twice with no changes re-applies without error", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{
			composeProjectLabel:    "myproject",
			composeConfigHashLabel: "somehash",
			composeWorkingDirLabel: "/home/user/myproject",
		})
		r, fakeClient := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.createCalls, 1)
		assert.Equal(t, fakeClient.statusUpdateCalls, 1)

		_, err = reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		// Create is attempted every reconcile, but the second attempt is a
		// no-op (AlreadyExists is ignored) since the object already exists;
		// the status is unconditionally re-applied on every reconcile
		// regardless of whether anything changed.
		assert.Equal(t, fakeClient.createCalls, 2)
		assert.Equal(t, fakeClient.updateCalls, 0)
		assert.Equal(t, fakeClient.statusUpdateCalls, 2)
	})

	t.Run("removes membership when a labeled container is deleted", func(t *testing.T) {
		t.Parallel()
		resource := newContainer(k8sNamespace, "r1", dockerNamespace, map[string]string{
			composeProjectLabel:    "myproject",
			composeConfigHashLabel: "somehash",
		})
		r, fakeClient := newReconciler(t, resource)

		_, err := reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)

		assert.NilError(t, r.Delete(t.Context(), resource))
		fakeClient.statusUpdateCalls = 0

		_, err = reconcileKind(r, v1alpha1.ContainerKind, client.ObjectKeyFromObject(resource), resource.GetUID())
		assert.NilError(t, err)
		assert.Equal(t, fakeClient.statusUpdateCalls, 1)

		var compose v1alpha1.ComposeProject
		assert.NilError(t, r.Get(t.Context(), keyFromProjectName("myproject"), &compose))
		assert.Equal(t, len(compose.Status.Containers), 0)
	})
}

func TestComposeConfigFiles(t *testing.T) {
	t.Parallel()

	type testCase struct {
		raw        string
		workingDir string
		want       []string
	}

	tests := []struct {
		name    string
		unix    testCase
		windows testCase
	}{
		{
			name: "empty input yields nil",
		},
		{
			name:    "unknown workingDir keeps paths unchanged",
			unix:    testCase{workingDir: "/a/b", raw: "/c/d.yaml,/e/f.yaml", want: []string{"/c/d.yaml", "/e/f.yaml"}},
			windows: testCase{workingDir: `C:\a\b`, raw: `C:\c\d.yaml,C:\e\f.yaml`, want: []string{`C:\c\d.yaml`, `C:\e\f.yaml`}},
		},
		{
			name:    "paths under workingDir become relative",
			unix:    testCase{workingDir: "/a/b", raw: "/a/b/c.yaml,/a/b/sub/d.yaml", want: []string{"c.yaml", "sub/d.yaml"}},
			windows: testCase{workingDir: `C:\a\b`, raw: `C:\a\b\c.yaml,C:\a\b\sub\d.yaml`, want: []string{`c.yaml`, `sub\d.yaml`}},
		},
		{
			name:    "a path outside workingDir is kept absolute",
			unix:    testCase{workingDir: "/a/b", raw: "/a/b/c.yaml,/somewhere/else/d.yaml", want: []string{"c.yaml", "/somewhere/else/d.yaml"}},
			windows: testCase{workingDir: `C:\a\b`, raw: `C:\a\b\c.yaml,C:\somewhere\else\d.yaml`, want: []string{`c.yaml`, `C:\somewhere\else\d.yaml`}},
		},
		{
			name:    "empty paths are ignored",
			unix:    testCase{workingDir: "/a/b", raw: "/a/b/c.yaml,  ,/a/b/d.yaml,", want: []string{"c.yaml", "d.yaml"}},
			windows: testCase{workingDir: `C:\a\b`, raw: `C:\a\b\c.yaml,  ,C:\a\b\d.yaml,`, want: []string{`c.yaml`, `d.yaml`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if runtime.GOOS == "windows" {
				got := composeConfigFiles(tt.windows.workingDir, tt.windows.raw)
				assert.DeepEqual(t, got, tt.windows.want)
			} else {
				got := composeConfigFiles(tt.unix.workingDir, tt.unix.raw)
				assert.DeepEqual(t, got, tt.unix.want)
			}
		})
	}
}

func TestReconcile_dispatch(t *testing.T) {
	t.Parallel()

	t.Run("logs and does not error for an unknown kind", func(t *testing.T) {
		t.Parallel()
		r, _ := newReconciler(t)
		result, err := r.Reconcile(t.Context(), composeRequest{
			Kind:      "SomeUnknownKind",
			Namespace: "rancher-desktop",
			Name:      "whatever",
		})
		assert.NilError(t, err)
		assert.DeepEqual(t, result, ctrl.Result{})
	})

	t.Run("no-ops for ComposeKind", func(t *testing.T) {
		t.Parallel()
		r, fakeClient := newReconciler(t)
		result, err := r.Reconcile(t.Context(), composeRequest{
			Kind:      v1alpha1.ComposeProjectKind,
			Namespace: "rancher-desktop",
			Name:      "whatever",
		})
		assert.NilError(t, err)
		assert.DeepEqual(t, result, ctrl.Result{})
		assert.Equal(t, fakeClient.updateCalls, 0)
		assert.Equal(t, fakeClient.statusUpdateCalls, 0)
	})

	t.Run("dispatches ContainerKind to reconcileContainer", func(t *testing.T) {
		t.Parallel()
		container := newContainer("rancher-desktop", "c1", "moby", map[string]string{
			composeProjectLabel:    "myproject",
			composeConfigHashLabel: "somehash",
		})
		r, fakeClient := newReconciler(t, container)

		result, err := r.Reconcile(t.Context(), composeRequest{
			Kind:           v1alpha1.ContainerKind,
			NamespacedName: client.ObjectKeyFromObject(container),
			UID:            container.GetUID(),
		})
		assert.NilError(t, err)
		assert.DeepEqual(t, result, ctrl.Result{})
		assert.Equal(t, fakeClient.createCalls, 1)
		assert.Equal(t, fakeClient.statusUpdateCalls, 1)
	})
}
