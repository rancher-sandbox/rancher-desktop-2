// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package crds

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/util/testapiserver"
)

const widgetCRD = `
# The header comment decodes to an empty document.
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    listKind: WidgetList
    plural: widgets
    singular: widget
  scope: Namespaced
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        description: first
        x-kubernetes-preserve-unknown-fields: true
`

func TestApply(t *testing.T) {
	clientset, err := apiextensionsclientset.NewForConfig(testapiserver.Start(t))
	assert.NilError(t, err)
	c := clientset.ApiextensionsV1().CustomResourceDefinitions()
	ctx := t.Context()

	decoded, err := Decode(widgetCRD)
	assert.NilError(t, err)
	assert.Equal(t, len(decoded), 1)
	desired := decoded[0]

	outcome, err := Apply(ctx, c, desired.DeepCopy())
	assert.NilError(t, err)
	assert.Equal(t, outcome, Created)
	assert.NilError(t, WaitEstablished(ctx, c, desired.Name, 30*time.Second))

	desired.Spec.Versions[0].Schema.OpenAPIV3Schema.Description = "second"
	outcome, err = Apply(ctx, c, desired.DeepCopy())
	assert.NilError(t, err)
	assert.Equal(t, outcome, Updated)
	stored, err := c.Get(ctx, desired.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, stored.Spec.Versions[0].Schema.OpenAPIV3Schema.Description, "second")
}
