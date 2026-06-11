# Environment Variables

RDD uses environment variables prefixed with `RDD_` for configuration and path discovery.

## Configuration Variables

These variables control RDD behavior. Set them before running `rdd` commands.

| Variable | Description | Default |
| --- | --- | --- |
| `RDD_INSTANCE` | Instance identifier. Determines which control plane and directories to use. Also settable via `rdd --instance`. | `2` |
| `RDD_DEVELOPER_MODE` | Enables developer mode: exposes hidden CLI flags, detects source tree for local builds. | unset |
| `RDD_KEEP_LOGS` | Preserves logs for post-mortem debugging. See [Log Preservation](#log-preservation) for details. | unset |
| `RDD_LOG_DIR` | Override the logging directory; usually used for tests. | unset |
| `RDD_LOG_LEVEL` | Sets the log level (`fatal`, `error`, `warn`, `info`, `debug`, `trace`). Overridden by `--log-level` flag. When unset, defaults to `debug` in developer mode, `warn` otherwise. | unset |
| `RDD_LOG_TITLE` | When set, writes this string as the first line of each new log file. Useful for identifying log files from specific test runs or sessions. | unset |
| `RDD_TRACE_PACKETS` | Adds `--trace-packets` to the guest vm-switch network setup, logging every packet sent or received. Requires `RDD_KEEP_LOGS`, and affects throughput and timing, so use it only when diagnosing network failures. | unset |
| `RDD_VM_CPUS` | Overrides the app VM's CPU count (the Lima template default is 2). Intended for CI, where runner sizing differs from user machines. A set-but-invalid value is an error, not a fallback. To be replaced by an App spec property. | unset |

### BATS Test Variables

These variables configure the BATS test framework. They have no effect on `rdd` itself.

| Variable | Description | Default |
| --- | --- | --- |
| `RDD_TRACE` | Enables verbose trace output in BATS tests. | `false` |
| `RDD_NAMESPACE` | Default Kubernetes namespace for BATS controller tests. | `rdd-bats` |
| `RDD_VM_TYPE` | Lima VM type for tests that boot a VM (`qemu` or `vz`). Useful for reproducing QEMU-specific failures on macOS. | Lima's default (`vz` on macOS, `qemu` on Linux) |

## Path Variables

`rdd svc paths --output=shell` exports these variables. They reflect the paths RDD uses for the current instance; setting them has no effect on RDD's behavior, other than `RDD_LOG_DIR` as listed above.

```shell
source <(rdd svc paths --output=shell)
```

| Variable | Description | Example (macOS, instance `2`) |
| --- | --- | --- |
| `RDD_ARGS_FILE` | Saved startup arguments | `~/Library/Application Support/rancher-desktop-2/args.json` |
| `RDD_DIR` | Service instance directory | `~/Library/Application Support/rancher-desktop-2` |
| `RDD_CONFIG` | RDD control plane config file | `~/Library/Application Support/rancher-desktop-2/config.yaml` |
| `RDD_K3S_CONFIG` | Mirror of the in-VM k3s kubeconfig | `~/Library/Application Support/rancher-desktop-2/k3s.yaml` |
| `RDD_LIMA_HOME` | Lima home directory | `~/.rd2/lima` |
| `RDD_LOG_DIR` | Log directory | `~/Library/Logs/rancher-desktop-2` |
| `RDD_PID_FILE` | Service PID file | `~/Library/Application Support/rancher-desktop-2/rdd.pid` |
| `RDD_SHORT_DIR` | Short directory path | `~/.rd2` |
| `RDD_TLS_DIR` | TLS certificate directory | `~/Library/Application Support/rancher-desktop-2/tls` |

The short directory (`RDD_SHORT_DIR`) exists because Lima uses Unix domain sockets with a 104-byte path limit. Placing `LIMA_HOME` under `~/.rd2/lima` instead of the full application support path keeps socket paths short enough.

Path variables are listed alphabetically. To retrieve a single path, pass the key name (lowercase, without the `RDD_` prefix):

```shell
rdd svc paths log_dir
```

## Log Preservation

When `RDD_KEEP_LOGS` is set to any non-empty value, RDD preserves logs that would otherwise be lost:

- **No pruning.** Log rotation normally keeps the five most recent backups and deletes older ones. With `RDD_KEEP_LOGS`, all numbered backups are retained.
- **Survives `svc delete`.** `rdd svc delete` normally removes the log directory. With `RDD_KEEP_LOGS`, the log directory is preserved.
- **Instance logs survive VM deletion.** When a LimaVM resource is deleted, Lima removes the instance directory (which contains hostagent and serial console logs). With `RDD_KEEP_LOGS`, the controller moves all `.log` files to a subdirectory of `RDD_LOG_DIR` named after the instance before deletion. If the subdirectory already exists (from a previous deletion), a numbered suffix is used (e.g., `opensuse.2/`).

### Log rotation

Both service logs (`rdd.stdout`, `rdd.stderr`) and hostagent logs (`ha.stdout`, `ha.stderr`) are rotated on each start: the active file is renamed to a numbered backup (e.g., `ha.stderr.1.log`), and a fresh file is created. Serial console logs (`serial`, `serialp`, `serialv`) are rotated the same way, but no new file is created — the VM driver creates it.

### BATS defaults

BATS tests set `RDD_KEEP_LOGS=1` by default (in `bats/helpers/defaults.bash`), so all test logs are preserved for CI artifact collection. The collection script (`scripts/collect-bats-logs.sh`) gathers both service logs and preserved instance logs into a single output directory.
