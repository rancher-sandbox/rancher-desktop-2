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
    updates:
      enabled: true
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

- **spec.application.updates.enabled**: For use by the Electron front end, to control whether the
  application will attempt to update itself.  Defaults to `true`.

- **spec.namespace**: The namespace where the owned `LimaVM` and its ConfigMaps are created. Defaults to `default`. **Immutable after creation** — changing it would orphan resources in the original namespace.

- **spec.running**: Set to `true` to start the LimaVM, `false` to stop it. The App controller propagates this value to `LimaVM.spec.running` on every reconcile.

**spec.containerEngine.name**: The container engine to use inside the VM. Valid values: `moby` (Docker-compatible, default) and `containerd`. Propagated to the `CONTAINER_ENGINE` Lima template param.

- **spec.kubernetes.enabled**: Whether Kubernetes should be enabled in the VM. Defaults to `false`. Propagated to the `KUBERNETES_ENABLED` Lima template param.

- **spec.kubernetes.version**: The Kubernetes version to use (e.g. `"1.30.2"`). Defaults to `"1.30.2"`. Propagated to the `KUBERNETES_VERSION` Lima template param.

- **status.kubernetesPort**: The host TCP port allocated for the k3s API server (`7441 + instance.Index()` by default). Set by the App reconciler on the first reconcile after `spec.kubernetes.enabled` becomes `true`, and cleared when `spec.kubernetes.enabled` is set back to `false` so that a fresh port is resolved on the next enable. The `KUBERNETES_PORT` Lima template param is set to this value; Lima's identity port-forward rule binds the same port on the host and forwards it to the guest.

- **status.conditions**: Multiple controllers write here. The App controller mirrors `Created` and `Running` from the owned `LimaVM` and computes `Settled`, the engine controller writes `ContainerEngineReady`, and the Kubernetes controller writes `KubernetesReady`. All writers use `retry.RetryOnConflict` with a re-Get so concurrent status updates do not 409.

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
  | `ContainerEngineReady` | `True`    | `NotApplicable`  | Mirroring is not implemented for the current backend (e.g. `containerd`); forced `True` so `rdd set` can finish waiting |
  | `ContainerEngineReady` | `False`   | `ConnectFailed`  | Engine controller failed to connect to Docker                     |
  | `ContainerEngineReady` | `False`   | `Stopped`        | The VM is stopped; the engine watcher is not running              |
  | `KubernetesReady`      | `True`    | `Ready`          | API server answers, node Ready, context merged into `~/.kube/config`. Workload-level readiness (coredns, traefik) is not gated; wait for those Deployments directly when needed |
  | `KubernetesReady`      | `False`   | `NotApplicable`  | `spec.kubernetes.enabled` is false                                |
  | `KubernetesReady`      | `False`   | `NotRunning`     | VM is not running, so k3s cannot be healthy                       |
  | `KubernetesReady`      | `False`   | `Probing`        | Waiting for k3s API server to respond                             |
  | `KubernetesReady`      | `False`   | `WaitingForNode` | API server answers but no node has reached Ready                  |
  | `KubernetesReady`      | `False`   | `MergeFailed`    | k3s is reachable but merging the instance kubeconfig failed (see `message`) |
  | `Settled`              | `True`    | `Settled`        | Reconcile chain has caught up with the current spec               |
  | `Settled`              | `False`   | `WaitingForLimaVM` | The App has no `Running` condition yet (nothing mirrored from LimaVM) |
  | `Settled`              | `False`   | `WaitingForEngine` | Engine controller has not yet written `ContainerEngineReady`    |
  | `Settled`              | `False`   | `EngineStale`    | Engine controller has not yet observed the current generation     |
  | `Settled`              | `False`   | `WaitingForKubernetes` | Kubernetes controller has not yet written `KubernetesReady`  |
  | `Settled`              | `False`   | `KubernetesStale` | Kubernetes controller has not yet observed the current generation |
  | `Settled`              | `False`   | `ApplyingTemplate` | A spec change rewrote the template; the LimaVM has not yet restarted into it |
  | `Settled`              | `False`   | *(forwarded)*    | Forwarded from the blocking `Running`, `ContainerEngineReady`, or `KubernetesReady` reason |

  `Running=True` means the Lima guest has finished booting and SSH is reachable. It says nothing about the container engine socket; consumers that depend on the engine (container/image/volume mirrors, `docker` against the forwarded socket) must also check `ContainerEngineReady`, which flips to `True` only after the engine controller has connected to the socket and completed its initial full sync.

  Anything waiting for the control plane to be ready should wait for `Settled=True`. Reaching it requires the following, in the order they become true:

  - The LimaVM must be up to date with the current template ConfigMap; otherwise `Settled` is `False`/`ApplyingTemplate`. LimaVM defers `status.observedTemplateResourceVersion` until the restart completes, and the App reconciler gates `Settled` on that field matching the template ConfigMap's `resourceVersion`.
  - The LimaVM must be running, reported by the mirrored `Running` condition. `Created` and `Running` are both mirrored from LimaVM, including their `lastTransitionTime`, so the timestamps track the LimaVM transition.
  - `ContainerEngineReady` must be `True`. The engine reconciler stamps its `observedGeneration` with the App's `metadata.generation` as it writes the condition, keeping the condition current with the App.
  - `KubernetesReady` must be `True` when `spec.kubernetes.enabled` is set. The Kubernetes reconciler stamps its `observedGeneration` the same way.

  `Settled` carries its own `observedGeneration`: the App's generation observed when the reconciler computed the condition. `rdd set` therefore waits for `Settled=True` with `observedGeneration` at least the post-patch `metadata.generation`, so it settles on its own change rather than a stale snapshot from an earlier generation.

Deleting the `App` resource triggers the finalizer to stop and delete the owned LimaVM (and wait for the LimaVM controller to complete its own cleanup before removing the App finalizer).

## GUI

How the GUI uses the App object:

### Status Bar

The status bar is updated with the information from the `status` part of the `App` object
