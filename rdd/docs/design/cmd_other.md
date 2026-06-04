# Other Commands

## `rdd kubectl`

Runs the regular `kubectl` program. Since `rdd` needs to be a kube client anyways, this functionality is basically available for free (at least from a code size point of view).

`rdd` is a multi-call binary (like `busybox`), so `rdd kubectl` can also be invoked via a symlink called `kubectl`.

There is also the [rdd ctl](cmd_service.md#rdd-ctl) command that automatically sets the kube config and context to the rdd control plane before executing `kubectl`.

### Version selection

`rdd kubectl` runs a kubectl that supports every feature of the cluster it talks to. An older kubectl is *compatible* with a newer server — the official skew policy allows ±1 minor version — but cannot drive features added in the server's minor version. The rule is therefore stricter: **the kubectl minor version must equal the server's, or be one higher**. Patch versions are ignored.

For every invocation that may contact a cluster, `rdd kubectl` probes the server version (with a 2-second timeout) and picks the first acceptable kubectl from:

1. The embedded kubectl.
2. The highest version in the download cache (`rdd svc paths kubectl_cache`).
3. A fresh download of the latest patch release of the server's minor version, looked up via the mirror's `stable-<major>.<minor>.txt` marker (e.g. `stable-1.34.txt` → `v1.34.9`), sha512-verified, and stored in the cache.

When the cluster is unreachable, and for client-only commands (`kubectl config`, `completion`, ...), the embedded kubectl runs. It is also the final fallback when nothing acceptable is cached and the version marker cannot be fetched (offline, or a pre-GA minor with no marker yet): rdd warns and runs it anyway, since rdd embeds a recent kubectl and Kubernetes rarely breaks backward compatibility. Once the marker has named a version, a failed or corrupted download aborts the command rather than hiding the problem. [rdd ctl](cmd_service.md#rdd-ctl) skips the probe entirely: the embedded apiserver always matches the embedded kubectl.

`RDD_KUBECTL_MIRROR` overrides the release mirror (default `https://dl.k8s.io`) and `RDD_CACHE_DIR` the cache root; see [environment.md](environment.md).

## `rdd cache`

TBD

## `rdd snapshot`

TBD

## `rdd package`

TBD
