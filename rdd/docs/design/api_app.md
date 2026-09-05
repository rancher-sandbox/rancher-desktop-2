# Rancher Desktop Application API

The `App` object is part of the `app.rancherdesktop.io` API group.

## App Components

### Singleton

There can be only a single `App` object in an RDD instance. It is **cluster-scoped** and must be named `app`.

Both the [rdd start](cmd_app.md#rdd-start) command and the [GUI](gui.md) app create the cluster-scoped `App` object, setting its `spec.namespace` to the configured "app-namespace" stored in the `config` ConfigMap in the `rdd-system` namespace (`rancher-desktop` by default)[^hardcoded].

[^hardcoded]: The "app-namespace" is only configurable so that it can be tested that the namespace isn't hardcoded anywhere in the controller.

Multiple versions of "Rancher Desktop 2" can be run in parallel by using different RDD instances, e.g.

```shell
RDD_INSTANCE=test rdd start --kube-version=1.35.1
```

The GUI will still be a system-wide singleton and only communicate with the `App` in a single RDD instance at a time. It _may_ support a submenu in the notification icon to switch between RDD instances.

### Lima VM

The `App` will create a `LimaDisk` and have it automatically mounted on a `LimaVM`.

#### Instance name

The `LimaVM` instance name is **always** `rd`. That means the Lima instance directory will be `~/.rd2/lima/rd`.

#### Data Disk

All user data is stored on the `LimaDisk`. Which means all images and also all local-path-storage.

Lightweight app snapshots only copy this data disk, and not the full VM image.

### Docker and Kube Contexts

When the `App` is starting it creates the Docker context and sets up the kubeconfig in `~/.kube/config`.

It will only change the current context if it does not exist, or is not working at the time the app is starting.

A standalone kube config holding only the `rancher-desktop-2` context is also written to `~/.rd2/kube.config`, which the [`rdd run`](cmd_app.md#rdd-run) command points `KUBECONFIG` at.

Consider using `cliPluginsExtraDirs` in `~/.docker/config.json` instead of installing into `~/.docker/cli-plugins` and have a diagnostic if the plugins exist in `~/.docker/cli-plugins`? The mechanism should be compatible with whatever we do on Windows.

## App object

### Example

```yaml
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app

spec:
  application:
    locale: en-zz
    updates:
      enabled: true
    addPath: manual
  containerEngine:
    name: moby
  kubernetes:
    enabled: false
    version: 1.32.2
  running: true
  namespace: rancher-desktop

status:
  kubernetesPort: 7443
  conditions:
  - type: Created
    status: "True"
    reason: Created
    message: Lima instance created successfully
    lastTransitionTime: "2024-01-01T00:00:00Z"
    observedGeneration: 1
  - type: Running
    status: "True"
    reason: Started
    message: Lima instance is running
    lastTransitionTime: "2024-01-01T00:00:05Z"
    observedGeneration: 1
  - type: Settled
    status: "True"
    reason: Settled
    message: App has reached the desired state
    observedGeneration: 1
```

- **spec.application.locale**: The display language to be used by the application; this may also
  affect other text for display.  Defaults to `en-us`.

- **spec.application.updates.enabled**: For use by the Electron front end, to control whether the
  application will attempt to update itself.  Defaults to `true`.

- **spec.application.addPath**: Controls whether and where the Rancher Desktop bin directory
  (`~/.rd<suffix>/bin`) is added to the user's `PATH`. The path-management controller edits the
  user's shell startup files on Unix (`.bash_profile`/`.bash_login`/`.profile`, `.bashrc`,
  `.zshrc`, `.cshrc`/`.tcshrc`, and fish's `config.fish`) and the user `Environment` in the
  registry on Windows. Valide values:
  - `front`: prepend, so the bin directory takes precedence (`bin:$PATH`).
  - `back`: append, so existing `PATH` entries win (`$PATH:bin`).
  - `manual`: do not manage `PATH`; remove any lines a previous `front`/`back` added.

  Defaults to `manual`, so instances created by the CLI never edit shell startup files without user's
  user's knowledge; the GUI is expected to send `front` explicitly when the user opts in.

  On Unix, `manual` removes only the block between our start/end markers, so a hand-written
  `PATH` line survives anywhere. On Windows the user `Environment` `Path` is a plain
  semicolon-separated list with nowhere to fence our entry, so under `manual` we can't tell an
  entry we added from one the user typed. We remove the bin directory only when it sits at the
  very front or back of `Path` (the two spots `front`/`back` would have written) and leave a copy
  placed in the middle alone. The trade-off: an entry parked at either end is treated as ours and
  removed, so to put the bin directory at the front or back on Windows use `front`/`back` rather
  than placing it there under `manual`.

- **spec.namespace**: The namespace where the owned `LimaVM` and its ConfigMaps are created. Defaults to `default`. **Immutable after creation** — changing it would orphan resources in the original namespace.

- **spec.running**: Set to `true` to start the LimaVM, `false` to stop it. The App controller propagates this value to `LimaVM.spec.running` on every reconcile.

- **spec.containerEngine.name**: The container engine to use inside the VM. Valid values: `moby` (Docker-compatible, default) and `containerd`. Propagated to the `CONTAINER_ENGINE` Lima template param.

- **spec.kubernetes.enabled**: Whether Kubernetes should be enabled in the VM. Defaults to `false`. Propagated to the `KUBERNETES_ENABLED` Lima template param.

- **spec.kubernetes.version**: The Kubernetes version to use (e.g. `"1.30.2"`). Defaults to `"1.30.2"`. Propagated to the `KUBERNETES_VERSION` Lima template param.

- **status.kubernetesPort**: The host TCP port allocated for the k3s API server (`7441 + instance.Index()` by default). Set by the App reconciler on the first reconcile after `spec.kubernetes.enabled` becomes `true`, and cleared when `spec.kubernetes.enabled` is set back to `false` so that a fresh port is resolved on the next enable. The `KUBERNETES_PORT` Lima template param is set to this value; Lima's identity port-forward rule binds the same port on the host and forwards it to the guest.

- **status.supportsNamespaces**: `true` when the selected container engine scopes containers and images into namespaces (`containerd`), `false` when it does not (`moby`). The engine controller writes it together with the `ContainerEngineReady` condition, so the UI can hide its container-namespace selector. It is also `false` whenever that condition's reason is `NotApplicable`, since a backend that mirrors nothing offers no namespaces to choose from. The field is absent until the engine controller first writes it; treat absence as unknown.

- **status.conditions**: Multiple controllers write here. The App controller mirrors `Created` and `Running` from the owned `LimaVM` and computes `Settled`, the engine controller writes `ContainerEngineReady`, the Kubernetes controller writes `KubernetesReady`, and the PATH management controller writes `PathManagementReady`. All writers use `retry.RetryOnConflict` with a re-Get so concurrent status updates do not 409.

  | Type                   | Status    | Reason           | Description                                                       |
  |------------------------|-----------|------------------|-------------------------------------------------------------------|
  | `Created`              | `Unknown` | `Pending`        | LimaVM controller has started reconciliation                      |
  | `Created`              | `True`    | `Created`        | Lima instance created on disk and ready                           |
  | `Created`              | `False`   | `CreateFailed`   | Lima instance creation failed (see `message` for details)         |
  | `Running`              | `Unknown` | `Reconciling`    | Verifying instance state (e.g. after controller restart)          |
  | `Running`              | `True`    | `Started`        | Lima instance is running                                          |
  | `Running`              | `False`   | `Stopped`        | Lima instance is stopped                                          |
  | `Running`              | `False`   | `Starting`       | Lima instance is starting up                                      |
  | `Running`              | `False`   | `StartFailed`    | Lima instance failed to start                                     |
  | `Running`              | `False`   | `StopFailed`     | Lima instance failed to stop cleanly                              |
  | `ContainerEngineReady` | `True`    | `Connected`      | Engine controller has connected to Docker and completed full sync |
  | `ContainerEngineReady` | `True`    | `NotApplicable`  | Mirroring is not supported for the selected engine on this platform (containerd on Windows); forced `True` so `rdd set` can finish waiting |
  | `ContainerEngineReady` | `False`   | `ConnectFailed`  | Engine controller failed to connect to Docker                     |
  | `ContainerEngineReady` | `False`   | `Stopped`        | The VM is stopped; the engine watcher is not running              |
  | `KubernetesReady`      | `True`    | `Ready`          | API server answers, node Ready, context merged into `~/.kube/config`. Workload-level readiness (coredns, traefik) is not gated; wait for those Deployments directly when needed |
  | `KubernetesReady`      | `False`   | `NotApplicable`  | `spec.kubernetes.enabled` is false                                |
  | `KubernetesReady`      | `False`   | `NotRunning`     | VM is not running, so k3s cannot be healthy                       |
  | `KubernetesReady`      | `False`   | `Probing`        | Waiting for k3s API server to respond                             |
  | `KubernetesReady`      | `False`   | `WaitingForNode` | API server answers but no node has reached Ready                  |
  | `KubernetesReady`      | `False`   | `MergeFailed`    | k3s is reachable but merging the instance kubeconfig failed (see `message`) |
  | `PathManagementReady`  | `True`    | `Applied`        | `spec.application.addPath` was applied for the current generation |
  | `PathManagementReady`  | `False`   | `Failed`         | Editing a shell startup file or the user Environment failed with a repairable error, e.g. I/O (see `message`) |
  | `PathManagementReady`  | `False`   | `Malformed`      | A startup file was hand-edited into an unparseable state (a lone marker, or markers in the wrong order); only the user can fix it |
  | `Settled`              | `True`    | `Settled`        | Reconcile chain has caught up with the current spec               |
  | `Settled`              | `False`   | `WaitingForLimaVM` | The App has no `Running` condition yet (nothing mirrored from LimaVM) |
  | `Settled`              | `False`   | `WaitingForEngine` | Engine controller has not yet written `ContainerEngineReady`    |
  | `Settled`              | `False`   | `EngineStale`    | Engine controller has not yet observed the current generation     |
  | `Settled`              | `False`   | `WaitingForKubernetes` | Kubernetes controller has not yet written `KubernetesReady`  |
  | `Settled`              | `False`   | `KubernetesStale` | Kubernetes controller has not yet observed the current generation |
  | `Settled`              | `False`   | `WaitingForPathManagement` | PATH management controller has not yet written `PathManagementReady` |
  | `Settled`              | `False`   | `PathManagementStale` | PATH management controller has not yet observed the current generation |
  | `Settled`              | `False`   | `ApplyingTemplate` | A spec change rewrote the template; the LimaVM has not yet restarted into it |
  | `Settled`              | `False`   | *(forwarded)*    | Forwarded from the blocking `Running`, `ContainerEngineReady`, `KubernetesReady`, or `PathManagementReady` reason (the latter under `front`/`back`, or under `manual` for a repairable `Failed`) |

  `Running=True` means the Lima guest has finished booting and SSH is reachable. It says nothing about the container engine socket; consumers that depend on the engine (container/image/volume mirrors, `docker` against the forwarded socket) must also check `ContainerEngineReady`, which flips to `True` only after the engine controller has connected to the socket and completed its initial full sync.

  Anything waiting for the control plane to be ready should wait for `Settled=True`. Reaching it requires the following, in the order they become true:

  - The LimaVM must be up to date with the current template ConfigMap; otherwise `Settled` is `False`/`ApplyingTemplate`. LimaVM defers `status.observedTemplateResourceVersion` until the restart completes, and the App reconciler gates `Settled` on that field matching the template ConfigMap's `resourceVersion`.
  - The LimaVM must be running, reported by the mirrored `Running` condition. `Created` and `Running` are both mirrored from LimaVM, including their `lastTransitionTime`, so the timestamps track the LimaVM transition.
  - `ContainerEngineReady` must be `True`. The engine reconciler stamps its `observedGeneration` with the App's `metadata.generation` as it writes the condition, keeping the condition current with the App.
  - `KubernetesReady` must be `True` when `spec.kubernetes.enabled` is set. The Kubernetes reconciler stamps its `observedGeneration` the same way.
  - `PathManagementReady` must be `True` when the PATH management controller is enabled, but the gate
    depends on `spec.application.addPath`. It runs host-side, independent of the VM, and stamps its
    `observedGeneration` with the App's `metadata.generation`. Freshness always gates: until the
    controller has observed the current generation (`PathManagementReady` absent or its
    `observedGeneration` behind), `Settled` is `False`/`WaitingForPathManagement` or
    `False`/`PathManagementStale`, so `rdd set --wait` can't report success before the edit runs —
    this holds even for `manual`, which still removes a block a previous `front`/`back` left behind.
    A *failure* holds `Settled` at `False` under `front`/`back`: there the user asked us to edit
    files, so a failed startup-file or user-Environment edit is surfaced. Under `manual` (the CRD
    default) it depends on whether the failure is repairable. A *repairable* failure
    (`PathManagementReasonFailed` — I/O, a read-only home, a full disk) over an intact block still
    gates: the reconciler requeues and clears it, so `rdd set --wait` shouldn't report success over a
    residue that's about to go away. A *permanent* failure (`PathManagementReasonMalformed` — a
    startup file the user hand-edited into an unparseable state) does not gate `Settled` under
    `manual`: the controller can't repair it and gating would wedge `Settled` forever. Either way the
    failure is recorded on `PathManagementReady`, so consumers should surface that condition rather
    than assume `Settled` covers it.

  `Settled` carries its own `observedGeneration`: the App's generation observed when the reconciler computed the condition. `rdd set` therefore waits for `Settled=True` with `observedGeneration` at least the post-patch `metadata.generation`, so it settles on its own change rather than a stale snapshot from an earlier generation.

Deleting the `App` resource triggers the finalizer to stop and delete the owned LimaVM (and wait for the LimaVM controller to complete its own cleanup before removing the App finalizer).

## GUI

How the GUI uses the App object:

### Status Bar

The status bar is updated with the information from the `status` part of the `App` object
