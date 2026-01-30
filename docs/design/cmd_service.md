# Service Commands

Service commands are for working with the control plane. The [Service API](api_service.md) document has additional information about the control plane internals, like startup and shutdown logic.

## Directories

### Service Directory

In this document all filenames are relative to the service directory `$APPDATA/rancher-desktop-$INSTANCE_NAME`, where `$INSTANCE_NAME` comes from the `RDD_INSTANCE` environment variable or the `--instance` flag.

This service directory contains the following files:

| File | Description |
| --- | --- |
| `config.json` | service config settings (written by `rdd service create` or `rdd service start`)
| `rdd.pid`     | pid of the control plane process (normally a background daemon) |
| `rdd.sqlite3` | control plane data store |

"Runs `rdd service ...`" means that the command performs the functionality in-progress, with the exception of `rdd service serve`, which will launch a background process.

### Short Directory

The short directory contains the following files and directories:

| File | Description |
| --- | --- |
| `bin`         | contains utilities like `docker`, `helm`, etc. May be symlinks |
| `kube.config` | Kubernetes config only containing the `rancher-desktop-2` context |
| `lima`        | `LIMA_HOME` is located here because of socket name length restrictions |

## `rdd service`

The control plane consists of the apiserver and the controller-manager.

It runs in a background process, which is started on demand by `rdd service serve`.

### `rdd service create`

Creates the service without starting it. This will rarely be used; the `rdd service start` command will call it automatically when the service does not yet exist.

Creates the service directory with the `rdd.sqlite3` data store. Stores the configuration settings in the `config.json` file.

Configuration options (incomplete):

*   `--controllers=*,-app`

    Specify which builtin controllers should be started. E.g during rapid development the `app`
    controller may be run as an external process, so the daemon doesn't need to be restarted all
    the time.

*   `--idle-timeout=5m`

    The daemon will automatically exit when no VMs are running, no snapshots are being saved or
    restored, and no API calls have been made for longer than the idle-timeout duration. Setting
    it to `0` disables automatic shutdown.

*   `--cache-directory=/Volumes/Cache/rdd2`

    Specify an alternate location for the cache directory in case the primary volume has
    insufficient free space.

    There will be similar options for the Lima and the snapshots directories.

    Changing directories for an existing instance will attempt to move the existing data
    to the new locations.

Additional configuration for individual controllers:

*   `--app-namespace=rancher-desktop`

    Specify the namespace for the Rancher Desktop app.


### `rdd service serve`

Backgrounds itself (fork && exec) unless invoked with `--background=false` for easier debugging.

Saves its own PID into `rdd.pid` and then starts the apiserver and the controller-manager using the configuration found in `config.json`.

Once the apiserver is running the config data will be copied into the `config` map in the `rdd-system` namespace. No controller should access `config.json` directly.


### `rdd service start`

Starts the service. Calls `rdd service create` if the service does not yet exist.

Takes all the same options as `rdd service create` and updates `config.json` with latest settings.

Runs `rdd service serve` to actually start the control plane in the background

Additional options:

*   `--no-wait`
    Return immediately and don't wait for all controllers to be ready. The user can force a wait
    later by running `rdd service start` without any options.


### `rdd service stop`

Sends SIGTERM signal to control plane process (`rdd.pid`).

### `rdd service delete`


### `rdd service reset`

Deletes the datastore, but create a new (empty) one with the same settings.


## `rdd service config`

Prints a kube config with context and service account[^sa] setup to give access to the RDD control plane.

[^sa]: A locked deployment profile will result in a service account with limited functionality to prevent bypassing the profile restrictions.

Runs `rdd service start` to ensure the `apiserver` is ready to accept requests.

## `rdd ctl`

Calls the RDD apiserver using the builtin `kubectl` code. It automatically starts the daemon and sets up the correct kubeconfig. It will ignore the `KUBECONFIG` environment variable:

```shell
rdd kubectl --kubeconfig=<(rdd service config) "$@"
```

Example to list all `vms` in all namespaces:

```shell
rdd ctl get vm -A
```

### `rdd ctl logs`

The `kubectl logs` command talks directly to `kubelet` to fetch container logs, so won't be able to return logs for a virtual machine. The `lima` controller will have to implement a custom `/logs` endpoint, and `rdd ctl logs` will not use the `kubectl logs` code, but a custom implementation.
