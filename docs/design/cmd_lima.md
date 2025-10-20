# LimaVM Commands

## `rdd limavm`

This command is similar to `limactl`, but uses `rdd` to create/start/stop/delete VM instances. It is a convenience command to work with other VMs and not needed to operate the Rancher Desktop app.

### `rdd limavm create NAME TEMPLATE --namespace NAMESPACE`

Create a new `LimaVM` instance `NAME` in `NAMESPACE` (or `default`) using `TEMPLATE`.

`TEMPLATE` should be the name of a ConfigMap inside the `NAMESPACE` where the resource should be created. It will be used as the `spec.templateRef.name` in the `LimaVM` resource. Referencing a ConfigMap in a different namespace is currently not supported and requires the use of `rdd ctl apply` instead.

`TEMPLATE` will be treated as a local filename if it contains a `/`.  In that case the `create` command will create a ConfigMap in `NAMESPACE` called `NAME`. It will store the "fully embedded" template from the file inside that ConfigMap and use it as the `spec.templateRef`. If a ConfigMap of this name already exists, then the `create` command will fail. If `LimaVM` creation succeeds then ownership of this ConfigMap is transferred to the `LimaVM` resource, and it will be deleted when the `LimaVM` instance is deleted. But when the command fails, then the ConfigMap is deleted immediately.

### `rdd limavm start NAME`

Set `spec.running` of the specified instance to `true`. There is no `--namespace` option because `LimaVM` names are globally unique (within the controlplane).

### `rdd limavm stop NAME`

Set `spec.running` of the specified instance to `false`.

### `rdd limavm delete NAME`

This will stop and delete the instance. The `LimaVM` resource will be deleted, which in turn will delete all owned resources. Currently, this is the `status.templateConfigMap`, and potentially the `spec.templateRef.name` template if it was created by `rdd lima create`.

### `rdd limavm params NAME1=VALUE1 NAME2=VALUE2`

Create/update/delete `spec.params` values. New entries are created as needed. Assigning an empty string will remove the name.

The `status.templateConfigMap` will be combined with the updated `spec.params` and needs to pass Lima validation. Otherwise, the update will be rejected.

If `spec.running` is true, then the instance will be restarted if the `params` have changed.

### `rdd limavm reset NAME`

Set `lima.rancherdesktop.io/resetRequested` annotation to the current timestamp. This tells the reconciler to delete the existing instance (stopping it, if necessary), and then recreate it using the same `status.templateConfigMap` and `spec.params` template.

### `rdd limavm restart NAME`

Set `lima.rancherdesktop.io/restartRequested` annotation to the current timestamp. This tells the reconciler to stop the instance (if it is running), and then to start it again.

### `rdd limavm shell NAME CMD`

Runs `CMD` inside a shell in the `NAME` instance, or opens an interactive shell if `CMD` is omitted.
