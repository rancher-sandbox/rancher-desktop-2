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

*   `--wait=false`
    Return immediately, without waiting for all controllers to be ready. Run `rdd service start`
    again with no options to wait.

*   `--timeout=90s`
    How long to wait for the control plane to become ready. Pass `0` to wait indefinitely.
    If the deadline expires, `rdd` exits with code 4.


### `rdd service stop`

Sends SIGINT to the control plane process (`rdd.pid`) and waits up to 5 minutes for
it to exit. On Windows the signal is delivered as `CTRL_BREAK_EVENT`. If the deadline
expires, the wait sends SIGTERM on Unix (or calls `TerminateProcess` on Windows)
as a fallback and `rdd` exits with code 4. Pass `--wait=false` to return immediately,
or `--timeout=0` to wait indefinitely. The default matches the per-VM ceiling because
graceful shutdown will eventually stop every running LimaVM before the service exits.

### `rdd service delete`

Stops the control plane and removes the instance directory, the short directory
(which contains the Lima home), and — unless `RDD_KEEP_LOGS` is set — the log
directory.

Delete always waits for the control plane to exit before removing files, because
removing the instance directory under a live process corrupts it on Windows and
breaks PID-file mutual exclusion on Unix. Use `--timeout` to bound that wait
(default `5m`); `0` waits indefinitely. If the deadline expires, `rdd` exits
with code 4 and the directory is left in place. See [`rdd service stop`](#rdd-service-stop)
for the signal and fallback details of the shutdown itself.

### `rdd service reset`

Deletes the datastore, but create a new (empty) one with the same settings.

### `rdd service status`

Show control plane status: whether it has been created, whether it is running, and its PID.

### `rdd service log`

Show control plane logs.

- `--stdout` (`-o`): Print stdout instead of stderr (default is stderr)
- `--follow` (`-f`): Follow log output

## `rdd service paths`

Prints instance directory and file paths. Accepts an optional key argument to print a single path.

| Key | Description |
| --- | --- |
| `dir` | Service directory (`$APPDATA/rancher-desktop-$INSTANCE`) |
| `log_dir` | Log directory |
| `short_dir` | Short directory (e.g. `~/.rd2`) |
| `lima_home` | Lima home directory (`$short_dir/lima`) |
| `tls_dir` | TLS certificate directory |
| `config` | RDD control plane config file path |
| `pid_file` | PID file path |
| `args_file` | Saved arguments file path |

Output formats (`--output`, `-o`):

*   `table` (default): aligned key-value table for human readability.

*   `json`: JSON object with all keys.

*   `shell`: `export` statements with `RDD_` prefix suitable for `source`, e.g. `export RDD_LOG_DIR="/path/to/logs"`.

With a key argument and table output, only the value is printed (no key prefix), so the result can be used directly in scripts.

Examples:

```console
$ rdd svc paths
args_file  /path/to/rancher-desktop-default/rdd.args
config     /path/to/rancher-desktop-default/config.json
dir        /path/to/rancher-desktop-default
lima_home  /path/to/.rd2/lima
log_dir    /path/to/rancher-desktop-default/log
pid_file   /path/to/rancher-desktop-default/rdd.pid
short_dir  /path/to/.rd2
tls_dir    /path/to/rancher-desktop-default/tls

$ rdd svc paths log_dir
/path/to/rancher-desktop-default/log

$ source <(rdd svc paths --output=shell)
```

## `rdd service config`

Prints a kube config with context and service account[^sa] setup to give access to the RDD control plane.

[^sa]: A locked deployment profile will result in a service account with limited functionality to prevent bypassing the profile restrictions.

This command will implicitly start the service, and will block until startup is complete.

The returned config will be static for the lifetime of the service, but may change the next time the service starts.

## `rdd ctl`

Calls the RDD apiserver using the builtin `kubectl` code. It automatically starts the daemon and sets up the correct kubeconfig. It will ignore the `KUBECONFIG` environment variable:

```shell
rdd kubectl --kubeconfig=<(rdd service config) "$@"
```

Example to list all `vms` in all namespaces:

```shell
rdd ctl get vm -A
```

### `rdd ctl wait-condition`

Waits for a resource condition to reach a specific state. Unlike `kubectl wait`, this command can filter by `lastTransitionTime` and condition `reason`.

```shell
rdd ctl wait-condition TYPE[.GROUP]/NAME CONDITION[=STATUS] [flags]
```

The `CONDITION[=STATUS]` positional argument names the condition type to match; `STATUS` defaults to `True`.

Flags:

- `--reason=REASON`: require the condition's `.reason` field to match
- `--since=TIMESTAMP|startup`: require `lastTransitionTime` after this value; `startup` reads the controller manager's start time from the discovery ConfigMap
- `--timeout=DURATION`: how long to wait (default `30s`)
- `--namespace`, `-n`: resource namespace (default `default`)

Examples:

```shell
# Wait for Running=True with reason Started, transitioned since controller startup
rdd ctl wait-condition limavm/default Running --reason=Started --since=startup --timeout=60s

# Wait for a condition after a specific timestamp
rdd ctl wait-condition limavm/default Running --since=2024-01-15T10:30:00Z

# Wait for Running=False
rdd ctl wait-condition limavm/default Running=False --timeout=60s
```

### `rdd ctl logs`

The `kubectl logs` command talks directly to `kubelet` to fetch container logs, so won't be able to return logs for a virtual machine. The `lima` controller will have to implement a custom `/logs` endpoint, and `rdd ctl logs` will not use the `kubectl logs` code, but a custom implementation.
