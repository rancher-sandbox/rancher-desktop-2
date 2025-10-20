# Lima API

The `lima.rancherdesktop.io` API group includes resources managed by Lima, including `LimaVM`, `LimaDisk`, and `LimaNetwork`.

## LimaVM

A `LimaVM` resource represents a VM managed by this `rdd` instance.

`LimaVM` resources can be created in different namespaces, but the VM names must be unique across the whole `rdd` instance.

Grouping VMs in namespace is useful for creating snapshots of related VMs, and for managing the lifecycle to stop pr delete all VMs inside a namespace.

### Example `LimaVM` object

```yaml
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: alpine
  namespace: default
  annotations:
    lima.rancherdesktop.io/resetRequested: "2025-10-12T08:00:00Z"
    lima.rancherdesktop.io/restartRequested: "2025-10-12T10:30:00Z"
spec:
  running: true
  templateRef:
    name: alpine
    namespace: lima-templates
  params:
    DOCKER_ROOTFUL: "true"
status:
  templateConfigMap: alpine-template
  conditions: []
```

- **metadata.annotations**: Can be used to request "actions" from the reconciler, like resetting (deleting and recreating with the same settings), or restarting the instance. The `status.conditions` can be used to store the state machine information.

- **spec.running**: Set to `true` when the instance should be running, set to `false` when it should be stopped.

- **spec.templateRef**: Specifies the `lima.yaml` template for the machine. The template must pass Lima validation or the `LimaVM` creation will fail. 

    Initially the only way to specify a template is via a ConfigMap that will store the "fully embedded" template under the `template` key. It cannot reference external base templates or scripts. This will change eventually when `spec.templateRef.name` can also be set to a URL. The default for `spec.templateRef.namespace` is the same as `metadata.namespace`.

    The `LimaVM` controller will create a new ConfigMap (in `metadata.namespace`) with the `metadata.name` and a `"-template"` suffix to store a copy of the validated template. This name is stored under `status.templateConfigMap`. The original `spec.templateRef` source is never accessed again after this, and can be modified or deleted without affecting the `LimaVM` resource. The `spec.templateRef` is immutable after creation and only serves as documentation.

    The `status.templateConfigMap` can be modified, but must pass Lima validation for the update to succeed. If `spec.running` is `true` and the template has changed, then the instance will be restarted. The `status.templateConfigMap` cannot be deleted, except by deleting the `LimaVM` resource itself, which will clean up owned resources automatically.

- **spec.params**: Override `spec.params` settings in the template. These values will be merged with the template before validation, and when creating/updating the `lima.yaml` file of the actual instance on disk.

    If the template provisioning scripts are properly parameterized, then the instance settings can be modified by just updating `spec.params`, which is simpler than modifying the `template` inside the ConfigMap. If `spec.running` is `true` then changing `spec.params` will restart the instance.

## LimaDisk

While a `LimaVM` object is specific to an OS, a `LimaDisk` object is just an `ext4` filesystem that can be copied between host operating systems. (Needs verification!)

## LimaNetwork

TBD
