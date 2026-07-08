// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"context"
	"maps"
	"testing"

	"gotest.tools/v3/assert"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	svccontrollers "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/service/controllers"
)

type countingClient struct {
	client.Client
	createCalls int
	updateCalls int
}

func (c *countingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.createCalls++
	return c.Client.Create(ctx, obj, opts...)
}

func (c *countingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.updateCalls++
	return c.Client.Update(ctx, obj, opts...)
}

func newReq() reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: svccontrollers.RDDSystemNamespace,
			Name:      k3sVersionsConfigMapName,
		},
	}
}

func newObjectMeta() metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Namespace: svccontrollers.RDDSystemNamespace,
		Name:      k3sVersionsConfigMapName,
	}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	assert.NilError(t, v1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func TestK3sVersionsReconciler(t *testing.T) {
	t.Run("Reconcile", func(t *testing.T) {
		t.Run("CreatesWhenMissing", func(t *testing.T) {
			base := newFakeClient(t)
			cc := &countingClient{Client: base}
			req := newReq()

			r := &K3sVersionsReconciler{
				Client: cc,
			}

			_, err := r.Reconcile(t.Context(), req)
			assert.NilError(t, err)
			assert.Equal(t, cc.createCalls, 1, "expected 1 create call")

			data, err := k3sVersionData()
			assert.NilError(t, err)

			cm := &v1.ConfigMap{}
			err = cc.Get(context.Background(), req.NamespacedName, cm)
			assert.NilError(t, err, "expected configmap to exist after reconcile")
			assert.DeepEqual(t, cm.Data, data)
		})
		t.Run("NoUpdateWhenDataMatches", func(t *testing.T) {
			data, err := k3sVersionData()
			assert.NilError(t, err)

			existing := &v1.ConfigMap{
				ObjectMeta: newObjectMeta(),
				Data:       maps.Clone(data),
			}
			existing.Labels = maps.Clone(DesiredLabels())
			base := newFakeClient(t, existing)
			cc := &countingClient{Client: base}

			r := &K3sVersionsReconciler{
				Client: cc,
			}

			_, err = r.Reconcile(t.Context(), newReq())
			assert.NilError(t, err)
			assert.Equal(t, cc.updateCalls, 0, "expected 0 update calls")
			assert.Equal(t, cc.createCalls, 0, "expected 0 create calls")
		})
		t.Run("UpdatesWhenDrifted", func(t *testing.T) {
			req := newReq()
			existing := &v1.ConfigMap{
				ObjectMeta: newObjectMeta(),
				Data: map[string]string{
					"versions": `["v1.30.0"]`,
					"channels": `{"stable":"v1.30.0"}`,
				},
			}
			existing.Labels = maps.Clone(DesiredLabels())
			base := newFakeClient(t, existing)
			cc := &countingClient{Client: base}

			r := &K3sVersionsReconciler{
				Client: cc,
			}

			_, err := r.Reconcile(t.Context(), req)
			assert.NilError(t, err)
			assert.Equal(t, cc.updateCalls, 1, "expected 1 update call")
			assert.Equal(t, cc.createCalls, 0, "expected 0 create calls")

			data, err := k3sVersionData()
			assert.NilError(t, err)

			cm := &v1.ConfigMap{}
			err = cc.Get(context.Background(), req.NamespacedName, cm)
			assert.NilError(t, err, "expected configmap to exist after reconcile")
			assert.DeepEqual(t, cm.Data, data)
		})
		t.Run("IgnoresExtraData", func(t *testing.T) {
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
			base := newFakeClient(t, existing)
			cc := &countingClient{Client: base}

			r := &K3sVersionsReconciler{
				Client: cc,
			}

			req := newReq()
			_, err = r.Reconcile(t.Context(), req)
			assert.NilError(t, err)
			assert.Equal(t, cc.updateCalls, 1, "expected 1 update call")
			assert.Equal(t, cc.createCalls, 0, "expected 0 create calls")

			cm := &v1.ConfigMap{}
			err = cc.Get(context.Background(), req.NamespacedName, cm)
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
