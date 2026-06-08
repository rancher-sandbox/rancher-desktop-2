# Application Commands

The application commands use short option names for usability. Unlike the `rdctl` tool from "Rancher Desktop 1" they will use e.g. `--cpus` instead of `--virtual-machine.number-cpus`.

## `rdd set [--dry-run] [--wait=BOOL] [--timeout=DURATION] PROPERTY=VALUE [PROPERTY=VALUE ...]`

Set one or more properties on the App singleton resource. Properties use dot notation for nested fields.

Valid property names and types are derived from the App CRD's OpenAPI schema at runtime, so the command automatically supports new properties as they are added to the CRD.

If the App resource does not exist, it is created with default settings before the specified values are applied.

By default, `rdd set` waits for the reconcile chain to settle before returning. Every property change — `running`, `containerEngine.name`, `kubernetes.enabled`, or any combination — waits for the App's `Settled` condition to reach `True` with `ObservedGeneration` matching the post-patch generation.

`Settled` goes `False` whenever the App controller sees a generation it has not yet caught up with, and back to `True` once the LimaVM has reached a terminal phase (Started or Stopped), the engine controller has processed the current generation, and — when `kubernetes.enabled` is true — the Kubernetes controller has confirmed the k3s API server and merged the kubeconfig.

The `ObservedGeneration` filter prevents a stale `Settled=True` from a previous reconcile from prematurely satisfying the wait.

- `--dry-run`: Validate changes against the API server's admission controller without persisting them. If the App does not exist, it is created with defaults (the VM will not start) so the admission controller can validate the patch. The wait is skipped in dry-run mode.
- `--wait`: Wait for the desired state after the patch is accepted (default `true`). Pass `--wait=false` to return as soon as the patch is accepted.
- `--timeout=DURATION`: How long to wait (default `5m`). `--timeout=0` waits indefinitely, matching `kubectl wait`.

Examples:

```shell
rdd set running=true
rdd set running=true containerEngine.name=containerd
rdd set kubernetes.enabled=true kubernetes.version=1.32.2
rdd set --dry-run running=true
rdd set --wait=false running=true
```

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success. |
| `1` | Generic / internal error. |
| `3` | The API server's admission controller rejected the request. |
| `4` | The wait deadline expired before the desired state was reached. |

`2` is reserved for cobra usage errors. Other rdd commands will adopt the same scheme as they grow `--wait` semantics; the codes are defined in `pkg/cli/exit`.

## `rdd start`

Start Rancher Desktop by setting `running=true` on the App singleton, creating the App with default settings if it does not yet exist. This is shorthand for `rdd set running=true`, sharing its `--wait`, `--timeout`, and exit-code behavior.

## `rdd stop`

Stop Rancher Desktop by setting `running=false` on the App singleton. If the App does not exist, `rdd stop` returns successfully without creating it. Otherwise it behaves like `rdd set running=false`, sharing the same `--wait` and `--timeout` flags.


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

`rdd run COMMAND [ARGS...]` runs a command against this Rancher Desktop instance without changing your selected contexts.

`rdd run` prepends `~/.rd2/bin` to `PATH`, sets the Docker context to `rancher-desktop-2`, and points `KUBECONFIG` at `~/.rd2/kube.config`, whose current context is `rancher-desktop-2`. It clears `DOCKER_HOST` so the Docker context takes effect. `rdd run` itself leaves your selected Docker context and the current context in `~/.kube/config` unchanged. Starting the App still merges the `rancher-desktop-2` entry into `~/.kube/config` and creates its Docker context; as a normal startup does, it switches your current context only when the existing one is missing or not working (see [api_app.md](api_app.md)).

Rancher Desktop starts first if it is not already running. When the App does not exist yet and the command is `kubectl` or `helm`, `rdd run` also enables Kubernetes so the command has a cluster to talk to; the App defaulter then picks the default version. `rdd run` only starts an existing App; it never reconfigures one.

For example, `rdd run docker run --rm hello-world` effectively executes:

```bash
rdd start
export PATH="$HOME/.rd2/bin:$PATH"
unset DOCKER_HOST
export DOCKER_CONTEXT=rancher-desktop-2
export KUBECONFIG="$HOME/.rd2/kube.config"  # current context is rancher-desktop-2
docker run --rm hello-world
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

