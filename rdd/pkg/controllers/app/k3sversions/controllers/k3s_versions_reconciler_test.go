// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"log/slog"
	"maps"
	"testing"

	"github.com/go-logr/logr"
	"gotest.tools/v3/assert"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/app/v1alpha1"
)

type spyingClient struct {
	client.Client
	createCalls int
	updateCalls int
}

func (c *spyingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCalls++
	return c.Client.Create(ctx, obj, opts...)
}

func (c *spyingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updateCalls++
	return c.Client.Update(ctx, obj, opts...)
}

func (c *spyingClient) GetByName(ctx context.Context, namespace, name string, obj client.Object) error {
	return c.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, obj)
}

func newFakeClient(t *testing.T, objs ...client.Object) *spyingClient {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, v1.AddToScheme(scheme))
	assert.NilError(t, v1alpha1.AddToScheme(scheme))

	return &spyingClient{
		Client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(objs...).
			Build(),
	}
}

func setLogger(t *testing.T) context.Context {
	t.Helper()

	logHandler := slog.NewTextHandler(t.Output(), &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return log.IntoContext(t.Context(), logr.FromSlogHandler(logHandler))
}

func TestK3sVersionsReconciler(t *testing.T) {
	const appName = "app-name"
	const targetNamespace = "target-namespace"
	app := &v1alpha1.App{
		ObjectMeta: metav1.ObjectMeta{
			// Apps are not namespaced
			Name: appName,
		},
		Spec: v1alpha1.AppSpec{
			Namespace: targetNamespace,
		},
	}

	// Create a new request to reconcile the app.
	newReq := func() reconcile.Request {
		return reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: appName,
			},
		}
	}
	// Create a new object meta for the config map.
	newObjectMeta := func() metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Namespace: targetNamespace,
			Name:      k3sVersionsConfigMapName,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: v1alpha1.GroupVersion.String(),
					Kind:       "App",
					Name:       appName,
					UID:        app.UID,
				},
			},
		}
	}

	t.Run("Reconcile", func(t *testing.T) {
		t.Run("CreatesWhenMissing", func(t *testing.T) {
			ctx := setLogger(t)
			cc := newFakeClient(t, app)
			req := newReq()

			r := &K3sVersionsReconciler{Client: cc}

			_, err := r.Reconcile(ctx, req)
			assert.NilError(t, err)
			assert.Equal(t, cc.createCalls, 1, "expected 1 create call")

			data, err := k3sVersionData()
			assert.NilError(t, err)

			cm := &v1.ConfigMap{}
			err = cc.GetByName(ctx, targetNamespace, k3sVersionsConfigMapName, cm)
			assert.NilError(t, err, "expected configmap to exist after reconcile")
			assert.DeepEqual(t, cm.Data, data)
		})

		t.Run("UpdatesWhenDrifted", func(t *testing.T) {
			ctx := setLogger(t)
			req := newReq()
			existing := &v1.ConfigMap{
				ObjectMeta: newObjectMeta(),
				Data: map[string]string{
					"versions": `["v1.30.0"]`,
					"channels": `{"stable":"v1.30.0"}`,
				},
			}
			existing.Labels = maps.Clone(desiredLabels)
			cc := newFakeClient(t, app, existing)

			r := &K3sVersionsReconciler{
				Client: cc,
			}

			_, err := r.Reconcile(ctx, req)
			assert.NilError(t, err)
			assert.Equal(t, cc.updateCalls, 1, "expected 1 update call")
			assert.Equal(t, cc.createCalls, 0, "expected 0 create calls")

			data, err := k3sVersionData()
			assert.NilError(t, err)

			cm := &v1.ConfigMap{}
			err = cc.GetByName(ctx, targetNamespace, k3sVersionsConfigMapName, cm)
			assert.NilError(t, err, "expected configmap to exist after reconcile")
			assert.DeepEqual(t, cm.Data, data)
		})

		t.Run("IgnoresExtraData", func(t *testing.T) {
			ctx := setLogger(t)
			data, err := k3sVersionData()
			assert.NilError(t, err)

			existing := &v1.ConfigMap{
				ObjectMeta: newObjectMeta(),
				Data: map[string]string{
					"extra": "data",
				},
			}
			existing.Labels = map[string]string{
				"extra": "label",
			}
			cc := newFakeClient(t, app, existing)

			r := &K3sVersionsReconciler{Client: cc}

			req := newReq()
			_, err = r.Reconcile(ctx, req)
			assert.NilError(t, err)
			assert.Equal(t, cc.updateCalls, 1, "expected 1 update call")
			assert.Equal(t, cc.createCalls, 0, "expected 0 create calls")

			cm := &v1.ConfigMap{}
			err = cc.GetByName(ctx, targetNamespace, k3sVersionsConfigMapName, cm)
			assert.NilError(t, err, "expected configmap to exist after reconcile")
			assert.Equal(t, cm.Labels["extra"], "label")
			for k, v := range desiredLabels {
				assert.Equal(t, cm.Labels[k], v)
			}
			assert.Equal(t, cm.Data["extra"], "data")
			for k, v := range data {
				assert.Equal(t, cm.Data[k], v)
			}
		})
	})
}
