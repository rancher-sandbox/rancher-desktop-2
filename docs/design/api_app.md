# Rancher Desktop Application API

The `App` object is part of the `app.rancherdesktop.io` API group.

## App Components

### Singleton

There can be only a single `App` object in an RDD instance.

Both the [rdd create](cmd_app.md#rdd-create) command and the [GUI](gui.md) app create the `App` object in the "app-namespace", which is a configuration setting stored in the `config` ConfigMap in the `rdd-system` namespace (`rancher-desktop` by default)[^hardcoded].

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
  name: rancher-desktop
  namespace: rancher-desktop

spec:
  containerEngine:
    name: moby
  kubernetes:
    version: 1.30.0
  running: true

status:
  containerEngine:
    name: moby
  kubernetes:
    version: 1.30.0

  progress: "Downloading Kubernetes 1.30.0"
  version: 2.0.0
  onlineStatus: true

  conditions:
  - type: AppCreated
    status: "True"
    reason: Created
    message: Rancher Desktop App created successfully
  - type: AppRunning
    status: "True"
    reason: Started
    message: Rancher Desktop App started successfully
```

- **spec**: Has all the usual fields that are in `settings.json` in "Rancher Desktop 1.x".

- **spec.running**: Set to `true` when the LimaVM should be running, set to `false` when it should be stopped.

- **status.conditions**: Standard Kubernetes conditions tracking the App state.

  | Type      | Status      | Reason         | Description                                                  |
  |-----------|-------------|----------------|--------------------------------------------------------------|
  | `Created` | `Unknown`   | `Pending`      | Reconciler has seen the resource; creation not yet attempted |
  | `Created` | `True`      | `Created`      | App has created the LimaVM and LimaDisk and is ready         |
  | `Created` | `False`     | `CreateFailed` | App creation failed                                          |
  | `Running` | `True`      | `Started`      | App is running                                               |
  | `Running` | `False`     | `Stopped`      | App is stopped                                               |
  | `Running` | `False`     | `StartFailed`  | App failed to start                                          |
  | `Running` | `False`     | `StopFailed`   | App failed to stop cleanly                                   |

Deleting the `App` resource triggers the finalizer to stop the and delete the LimaVM and the LimaDisk.

## GUI

How the GUI uses the App object:

### Status Bar

The status bar is updated with the information from the `status` part of the `App` object
