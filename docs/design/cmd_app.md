# Application Commands

The application commands use short option names for usability. Unlike the `rdctl` tool from "Rancher Desktop 1" they will use e.g. `--cpus` instead of `--virtual-machine.number-cpus`.

## `rdd set [--dry-run] [--timeout=DURATION] PROPERTY=VALUE [PROPERTY=VALUE ...]`

Set one or more properties on the App singleton resource. Properties use dot notation for nested fields.

Valid property names and types are derived from the App CRD's OpenAPI schema at runtime, so the command automatically supports new properties as they are added to the CRD.

If the App resource does not exist, it is created with default settings before the specified values are applied.

By default, `rdd set` waits for the desired state before returning:

- With `running=true`, it waits for `ContainerEngineReady=True` (the engine watcher has connected to Docker for the `moby` backend, or reports `NotApplicable` for `containerd`).
- With `running=false`, it waits for the App's `Running` condition to leave `True` — i.e. the VM has actually stopped, which is stricter than "container engine disconnected".
- Other property changes do not currently trigger a wait. Pure backend swaps (e.g. `rdd set containerEngine.name=containerd` without a `running` argument) return as soon as the patch is accepted.

The wait uses the App's `metadata.generation` after the patch to filter stale condition snapshots, so a leftover `ContainerEngineReady=True` from a prior backend cannot prematurely satisfy the wait.

- `--dry-run`: Validate changes against the API server's admission controller without persisting them. If the App does not exist, it is created with defaults (the VM will not start) so that the admission controller can validate the patch. The wait is skipped in dry-run mode.
- `--timeout=DURATION`: How long to wait (default `5m`). `--timeout=0` skips the wait entirely and returns as soon as the patch is accepted, preserving the pre-wait legacy behavior.

Examples:

```shell
rdd set running=true
rdd set running=true containerEngine.name=containerd
rdd set kubernetes.enabled=true kubernetes.version=1.32.2
rdd set --dry-run running=true
rdd set --timeout=0 running=true
```

## `rdd create`


## `rdd start`

```bash
rdd ctl patch app app --type=merge -p '{"spec":{"running":true}}'
```

## `rdd stop`

```bash
rdd ctl patch app app --type=merge -p '{"spec":{"running":false}}'
```


## `rdd delete`

Delete the `App` and all owned objects, like the `LimaVM` and the `K3sVersions`, etc.

Equivalent to

```bash
rdd ctl delete namespace rancher-desktop
```

## `rdd reset`

Set `App.status` to `stopped` and delete the `LimaVM`, but keep the `App` object. The VM will be recreated when the `spec.status` is set back to `running`.

## `rdd shell`

```bash
rdd lima shell rd "$@"
```

## `rdd run`

Set up `PATH` to start with `~/.rd$RDD_INSTANCE` and set the docker and kube contexts to `rancher-desktop` before running the command.

Since there are no environment variables for the contexts, it will have to set `DOCKER_HOST` and `KUBECONFIG` instead.

For example `rdd run docker images` will effectively execute:

```bash
export PATH="$HOME/.rd2/bin:$PATH"
export DOCKER_HOST="unix://$HOME/.rd2/docker.sock"
export KUBECONFIG="$HOME/.rd2/kube.config"
docker images
```

## `rdd shell-profile`

It prints a list of shell commands to STDOUT to put the `~/.rd2/bin` directory on the `PATH` and load completions for `rdd` and any bundled utilities (`docker`, `helm`, ...).

```console
$ rdd shell-profile bash --path --completions
export PATH="$HOME/.rd2/bin:$PATH"
source <(rdd completion bash)
source <(docker completions bash)
source <(helm completions bash)
...
```

This is also the command the "path management" inserts into the users shell profile, e.g.

```bash
source <(~/.rd2/bin/rdd shell-profile bash --path)
```

