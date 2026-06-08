# LimaVM Commands

## `rdd limavm`

This command is similar to `limactl`, but uses `rdd` to create/start/stop/delete VM instances. It is a convenience command to work with other VMs and not needed to operate the Rancher Desktop app. It is hidden (like `rdd ctl`) unless `rdd` is running in developer mode.

### `rdd limavm create NAME TEMPLATE --namespace NAMESPACE`

Create a new `LimaVM` instance `NAME` in `NAMESPACE` (or `default`) using `TEMPLATE`.

`TEMPLATE` should be the name of a ConfigMap inside the `NAMESPACE` where the resource should be created. It will be used as the `spec.templateRef.name` in the `LimaVM` resource. Referencing a ConfigMap in a different namespace is currently not supported and requires the use of `rdd ctl apply` instead.

`TEMPLATE` will be treated as a local filename or template URL if it contains a `/` or a `:`.  In that case the `create` command will create a ConfigMap in `NAMESPACE` called `NAME`. It will store the "fully embedded" template from the file or URL inside that ConfigMap and use it as the `spec.templateRef`. If a ConfigMap of this name already exists, then the `create` command will fail. If `LimaVM` creation succeeds then ownership of this ConfigMap is transferred to the `LimaVM` resource, and it will be deleted when the `LimaVM` instance is deleted. But when the command fails, then any ConfigMap created from a file or URL is deleted immediately.

- `--start` (default `false`): Set `spec.running=true` to start the VM immediately after creation.
- `--wait` (default `true`): When used with `--start`, wait until the Running condition becomes True before returning.
- `--timeout` (default `5m`): Maximum time to wait. A value of `0` waits indefinitely. Accepts Go duration strings (e.g., `30s`, `5m`). If the deadline expires, `rdd` exits with code 4.

### `rdd limavm start NAME`

Set `spec.running` of the specified instance to `true`. There is no `--namespace` option because `LimaVM` names are globally unique (within the control plane).

- `--wait` (default `true`): Wait until the Running condition becomes True before returning.
- `--timeout` (default `5m`): Maximum time to wait. A value of `0` waits indefinitely. If the deadline expires, `rdd` exits with code 4.

### `rdd limavm stop NAME`

Set `spec.running` of the specified instance to `false`.

- `--wait` (default `true`): Wait until the Running condition becomes False before returning.
- `--timeout` (default `5m`): Maximum time to wait. A value of `0` waits indefinitely. If the deadline expires, `rdd` exits with code 4.

### `rdd limavm delete NAME`

Stop and delete the instance. The `LimaVM` resource deletion triggers cleanup of all owned resources: the `status.templateConfigMap`, and the `spec.templateRef.name` template if `rdd limavm create` created it.

- `--wait` (default `true`): Wait until the resource is fully deleted before returning.
- `--timeout` (default `5m`): Maximum time to wait. A value of `0` waits indefinitely. If the deadline expires, `rdd` exits with code 4.

### `rdd limavm params NAME1=VALUE1 NAME2=VALUE2`

Create/update/delete `spec.params` values. New entries are created as needed. Assigning an empty string will remove the name.

The `status.templateConfigMap` will be combined with the updated `spec.params` and needs to pass Lima validation. Otherwise, the update will be rejected.

If `spec.running` is true, then the instance will be restarted if `spec.params` have changed.

### `rdd limavm reset NAME`

Set `lima.rancherdesktop.io/resetRequested` annotation to the current timestamp. This tells the reconciler to delete the existing instance (stopping it, if necessary), and then recreate it using the same `status.templateConfigMap` and `spec.params` template.

### `rdd limavm restart NAME`

Set `lima.rancherdesktop.io/restartRequested` annotation and `spec.running=true` in a single patch. The annotation tells the reconciler to stop the instance if it is running; setting `spec.running=true` ensures the instance starts afterward, even if it had been stopped initially.

- `--wait` (default `true`): Wait until `status.restartCount` increments (the instance has fully restarted) before returning.
- `--timeout` (default `5m`): Maximum time to wait. A value of `0` waits indefinitely. If the deadline expires, `rdd` exits with code 4.

### `rdd limavm shell NAME CMD`

Runs `CMD` inside a shell in the `NAME` instance, or opens an interactive shell if `CMD` is omitted.

### `rdd limavm logs NAME`

Show hostagent logs for the `NAME` instance.

- `--stdout` (`-o`): Print stdout instead of stderr (default is stderr)
- `--follow` (`-f`): Follow log output
