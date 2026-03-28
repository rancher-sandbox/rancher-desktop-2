# Rancher Desktop Application API

The `App` object is part of the `app.rancherdesktop.io` API group.

## App Components

### Singleton

There can be only a single `App` object in an RDD instance. It is **cluster-scoped** and must be named `app`.

Both the [rdd create](cmd_app.md#rdd-create) command and the [GUI](gui.md) app create the cluster-scoped `App` object, setting its `spec.namespace` to the configured "app-namespace" stored in the `config` ConfigMap in the `rdd-system` namespace (`rancher-desktop` by default)[^hardcoded].

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

The kube config is also written to `~/.rd2/kube.config` (mostly for the [`rdd run`](cmd_app.md#rdd-run) command).

Consider using `cliPluginsExtraDirs` in `~/.docker/config.json` instead of installing into `~/.docker/cli-plugins` and have a diagnostic if the plugins exist in `~/.docker/cli-plugins`? The mechanism should be compatible with whatever we do on Windows.

## App object

### Example

```yaml
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app

spec:
  containerEngine:
    name: moby
  kubernetes:
    version: 1.30.0
  running: true
  namespace: rancher-desktop

status:
  containerEngine:
    name: moby
  kubernetes:
    version: 1.30.0

  progress: "Downloading Kubernetes 1.30.0"
  version: 2.0.0
  onlineStatus: true

  conditions:
  - type: Created
    status: "True"
    reason: Created
    message: Lima instance created successfully
    lastTransitionTime: "2024-01-01T00:00:00Z"
  - type: Running
    status: "True"
    reason: Started
    message: Lima instance is running
    lastTransitionTime: "2024-01-01T00:00:05Z"
```

- **spec.namespace**: The namespace where the owned `LimaVM` and its ConfigMaps are created. Defaults to `default`. **Immutable after creation** — changing it would orphan resources in the original namespace.

- **spec.running**: Set to `true` to start the LimaVM, `false` to stop it. The App controller propagates this value to `LimaVM.spec.running` on every reconcile.

- **status.conditions**: Conditions are **mirrored from the owned `LimaVM`** resource. The App controller copies `type`, `status`, `reason`, `message`, and `lastTransitionTime` from the LimaVM's conditions.

  | Type      | Status      | Reason         | Description                                                       |
  |-----------|-------------|----------------|-------------------------------------------------------------------|
  | `Created` | `Unknown`   | `Pending`      | LimaVM controller has started reconciliation                      |
  | `Created` | `True`      | `Created`      | Lima instance created on disk and ready                           |
  | `Created` | `False`     | `CreateFailed` | Lima instance creation failed (see `message` for details)         |
  | `Running` | `Unknown`   | `Reconciling`  | Verifying instance state (e.g. after controller restart)          |
  | `Running` | `True`      | `Started`      | Lima instance is running                                          |
  | `Running` | `False`     | `Stopped`      | Lima instance is stopped                                          |
  | `Running` | `False`     | `Starting`     | Lima instance is starting up                                      |
  | `Running` | `False`     | `StartFailed`  | Lima instance failed to start                                     |
  | `Running` | `False`     | `StopFailed`   | Lima instance failed to stop cleanly                              |

  Because conditions are mirrored, `lastTransitionTime` reflects when the **LimaVM** transitioned, not when the App controller copied the value. This makes the timestamp meaningful for staleness checks.

Deleting the `App` resource triggers the finalizer to stop and delete the owned LimaVM (and wait for the LimaVM controller to complete its own cleanup before removing the App finalizer).

## GUI

How the GUI uses the App object:

### Status Bar

The status bar is updated with the information from the `status` part of the `App` object
