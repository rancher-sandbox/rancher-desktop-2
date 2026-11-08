# Other Commands

## `rdd kubectl`

Runs the regular `kubectl` program. Since `rdd` needs to be a kube client anyways, this functionality is basically available for free (at least from a code size point of view).

`rdd` is a multi-call binary (like `busybox`), so `rdd kubectl` can also be invoked via a symlink called `kubectl`. `rdd` runs only under the lowercase names `rdd`, `kubectl`, `nerdctl`, `yq` and [`rdd-<instance>`](README.md#side-by-side-operation), and under any name that starts with `rdd.` and a digit. That last rule covers the file name `rdd.<version>.<platform>.<arch>` that releases give the standalone `rdd`. On Windows each of these names can also end in `.exe`, in any letter case. Under any other name, such as `RDD` or `rdd.old` on any platform, or `kubectl.exe` on macOS, it exits with an error. On Linux, when `rdd` runs through a symlink, it starts its service and hostagent processes from the file that the symlink points to, so that file needs the name `rdd`, `rdd-<instance>` or a name that starts with `rdd.` and a digit.

There is also the [rdd ctl](cmd_service.md#rdd-ctl) command that automatically sets the kube config and context to the rdd control plane before executing `kubectl`.

### Version selection

`rdd kubectl` runs a kubectl that supports every feature of the cluster it talks to. An older kubectl is *compatible* with a newer server — the official skew policy allows ±1 minor version — but cannot drive features added in the server's minor version. The rule is therefore stricter: **the kubectl minor version must equal the server's, or be one higher**. Patch versions are ignored.

For an invocation that may contact a cluster, apart from the exceptions described below, `rdd kubectl` probes the server version (with a 2-second timeout) and picks the first acceptable kubectl from:

1. The embedded kubectl.
2. The highest version in the download cache (`rdd svc paths kubectl_cache`).
3. A fresh download of the latest patch release of the server's minor version, looked up via the mirror's `stable-<major>.<minor>.txt` marker (e.g. `stable-1.34.txt` → `v1.34.9`), sha512-verified, and stored in the cache.

The probe parses the command line through kubectl's own command tree, so it reads connection flags, kuberc defaults and the kuberc credential plugin policy the way the embedded kubectl does. A separate flag parser in rdd would get attached shorthand values such as `-ojson` wrong and would not see kuberc. In exchange, `rdd kubectl` stops on a kuberc that the embedded kubectl rejects, even when the kubectl it would run is older and ignores kuberc.

The probe can still measure a different cluster than the command contacts. It does not see the arguments that a kuberc alias adds, so it probes the cluster that the command line selects without them. When those arguments supply the value of a flag on the command line, the probe may fail to parse the command line, and the embedded kubectl then runs. And when a kuberc default selects the cluster, the kubectl that rdd picks may be too old to read kuberc, and then contacts the current context instead.

When the cluster is unreachable, and for client-only commands (`kubectl config`, `completion`, ...), the embedded kubectl runs. It is also the final fallback when nothing acceptable is cached and the version marker cannot be fetched within 5 seconds (offline, or a pre-GA minor with no marker yet): rdd warns and runs it anyway, since rdd embeds a recent kubectl and Kubernetes rarely breaks backward compatibility. Once the marker has named a version, a failed or corrupted download aborts the command rather than hiding the problem. [rdd ctl](cmd_service.md#rdd-ctl) skips the probe entirely: the embedded apiserver always matches the embedded kubectl.

Top-level kubectl plugins, unknown commands and shell-completion requests (`kubectl __complete`, which the shell runs on TAB) also run the embedded kubectl without a probe, because kubectl's command tree cannot find them. kubectl itself does not contact a cluster for a plugin, and the kubectl calls that the plugin makes resolve separately. The same holds for a plugin under `create`, such as kubectl-create-foo, which kubectl releases before 1.29 do not run by default. A top-level command or `create` subcommand that only a newer kubectl has counts as unknown, so the embedded kubectl rejects it. Completion skips the probe so that TAB never waits on the probe or on a download, whose progress the shell hides by discarding stderr. In exchange, completion offers what the embedded kubectl knows, such as its output formats, even when the command then runs an older kubectl.

`RDD_KUBECTL_MIRROR` overrides the release mirror (default `https://dl.k8s.io`) and `RDD_CACHE_DIR` the cache root; see [environment.md](environment.md).

## `rdd nerdctl`

Runs `nerdctl` inside the App's VM, against the shared containerd socket. Like `rdd kubectl`, it can also be invoked via a link called `nerdctl`.

It never starts the control plane or the App, and fails unless the App runs the containerd engine and that engine is ready. `rdd run nerdctl` starts the App first (see [rdd run](cmd_app.md#rdd-run)).

## `rdd cache`

TBD

## `rdd snapshot`

TBD

## `rdd package`

TBD
