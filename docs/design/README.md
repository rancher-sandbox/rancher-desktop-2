# Rancher Desktop Daemon (RDD)

Rancher Desktop Daemon is the backend for Rancher Desktop 2.0. It is written in Go and uses Lima to manage the virtual machine on all platforms including Windows.

RDD is distributed as a single `rdd` binary for each platform; it does not need an installer and embeds all required resources with the exception of the GUI app and any privileged helper executables.

## Basic Architecture

RDD is based on the Kubernetes control plane. It uses the kube API machinery and reconciling controllers to adjust its state. The control plane only includes standard controllers for Namespaces, ConfigMaps, ServiceAccounts, RBAC, and CRDs. It has no support for running containers and therefore can run natively on all platforms (Linux, macOS, Windows). Instead of `etcd` it uses [kine](https://github.com/k3s-io/kine) with SQLite as the datastore, just like `k3s`.

### Directories

The control plane has both a [service directory](cmd_service.md#service-directory) `$APPDATA/rancher-desktop-2` and a [short directory](cmd_service.md#short-directory) `~/.rd2`[^lima]. `$APPDATA` is the platform-specific application data directory, e.g. `~/Library/Application Support` on macOS.

[^lima]: RDD will set `LIMA_HOME` to `~/.rd2/lima` instead of `$APPDATA/rancher-desktop-2/lima` because of socket name length constraints.

It is currently not a goal to support RDD running as a system service; it is always tied to a user session[^WSL2]. Design decisions should still attempt to not make that harder, in case this ever becomes a goal.

[^WSL2]: On Windows WSL2 also depends on the user being logged in. It would require a Hyper-V Lima driver before it could run as a system service.

### Side-by-Side Operation

RDD can be installed side-by-side with "Rancher Desktop 1.x". They can be installed and uninstalled in any order and are completely ignorant of each other[^ports].

[^ports]: There may be port conflicts between the GUIs of "Rancher Desktop 1" and "Rancher Desktop 2", so the GUIs may not be able to run concurrently. Ideally this should be resolved.

Multiple instances of RDD (same or different versions) can be running at the same time; they will use separate service and path directories and are completely independent of each other[^profile].

Supporting multiple RDD instances in parallel allows developers to run BATS integration tests without interfering with their regular RDD configuration.

It also makes it possible to compare 2 different configurations running concurrently without having to switch back and forth using snapshots. This makes it easy to test a new release without affecting the current "production" version.

This is controlled by the `RDD_INSTANCE` environment variable (defaults to `2`) or the global `--instance` flag. With `RDD_INSTANCE=bats` or `rdd --instance=bats` the service directory becomes `$APPDATA/rancher-desktop-bats` and the short directory becomes `~/.rdbats`. <!-- spellchecker:ignore -->

Similarly the docker and kube contexts (normally `rancher-desktop-2`) become `rancher-desktop-bats`.

The `--instance` flag takes precedence over the `RDD_INSTANCE` environment variable if both are specified.

[^profile]: The only exception is the system [deployment profile](profile.md), which applies to all instances.

### Commands

`rdd` has many subcommands. Their documentation has been split into multiple documents:

* [Service commands](cmd_service.md) for managing the RDD control plane
* [Lima commands](cmd_lima.md) are an easier way to manage Lima VMs, disks, and networks
* [App commands](cmd_app.md) provide a CLI interface to "Rancher Desktop 2"
* [Other commands](cmd_other.md) for commands that don't fit the other categories

### API Groups / Controllers

RDD includes controllers for multiple API groups. Each group can be separately versioned, updated, disabled, etc. The [controller framework](controllers.md) documents the shared base infrastructure all groups use: registration, finalizers, owned-resource cleanup, and webhook helpers.

#### `rdd.rancherdesktop.io`

Provides basic infrastructure services. Examples include:

* [resource APIs](api_resource.md) for downloading, caching and locating files
* [url monitoring APIs](api_url_monitor.md) periodically check for new versions
* [diagnostics APIs](api_diagnostic.md) can raise warnings and errors from any component
* [service lifecycle](api_service.md) explains how the control plane start up and shuts down
* [controller manager discovery](discovery.md) explains how controller managers find each other
* [snapshot APIs](api_snapshot.md) can save and restore all or part of the state of the RDD service
* [deployment profiles](profile.md) set defaults and can lock down features

#### `lima.rancherdesktop.io`

Provides access to Lima VMs, disks, and networks. The [lima.rancherdesktop.io](api_lima.md) APIs makes use of the `rdd.rancherdesktop.io` APIs.

#### `app.rancherdesktop.io`

Implements the backend part of the Rancher Desktop 2.0 application (everything except for the GUI and for any kind of privileged helpers).

The [app.rancherdesktop.io](api_app.md) APIs use both the `rdd.rancherdesktop.io` and the `lima.rancherdesktop.io` APIs.

APIs include:

* [app API](api_app.md) is the "Rancher Desktop 2" application object itself
* [k3s version API](api_k3sversion.md) maintains a list of k3s versions resource objects

The [GUI application](gui.md) does not interact with the host machine except via the kube API of RDD (or the `containerd`/`dockerd`/`k3s` APIs of the corresponding services)[^remote].

[^remote]: This means that technically the GUI doesn't have to run on the same host as `rdd`. But this is not a goal. If this ever becomes necessary, it would more likely be implemented by an `rdd-shim` that would handle the remoting.

## CLI Operation

The GUI in Rancher Desktop 2.0 is completely optional. Providing a great CLI experience is one of the goals for `rdd`. The required steps to run `rdd` on a new machine should be as simple as

```shell
curl -L https://example.com/rdd-linux-x86_64 -o /usr/local/bin/rdd
chmod +x /usr/local/bin/rdd
rdd start
rdd run docker run hello-world
```

The [bootstrapping](bootstrap.md) page shows in detail how a new control plane will get created and started, and will then launch the "Rancher Desktop 2" application in turn.

## Order of implementation

The proposed order of [implementation](implementation.md) is optimized to get a working proof of concept as quickly as possible.
