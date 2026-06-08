# Other Commands

## `rdd kubectl`

Runs the regular `kubectl` program. Since `rdd` needs to be a kube client anyways, this functionality is basically available for free (at least from a code size point of view).

`rdd` is a multi-call binary (like `busybox`), so `rdd kubectl` can also be invoked via a symlink called `kubectl`.

There is also the [rdd ctl](cmd_service.md#rdd-ctl) command that automatically sets the kube config and context to the rdd control plane before executing `kubectl`.

## `rdd cache`

TBD

## `rdd snapshot`

TBD

## `rdd package`

TBD
