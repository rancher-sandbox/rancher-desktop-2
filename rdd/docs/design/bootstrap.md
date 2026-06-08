# Bootstrapping Rancher Desktop 2 from Scratch

## Install RDD on a new machine

RDD comes as a single `rdd` binary that should be put on the `PATH` for convenience.

```bash
curl -L https://example.com/rdd-linux-x86_64 -o /usr/local/bin/rdd
chmod +x /usr/local/bin/rdd
```

Running `rdd start` will now bootstrap Rancher Desktop 2. When it returns the user will be able to run `docker` commands.

## Starting the Control Plane and the App

Conceptually `rdd start` (with the default instance name `2` from `RDD_INSTANCE=2` or `--instance=2`) will trigger these actions:

* `rdd start` calls `rdd service config` to get a kube config to talk to the control plane.

  * `rdd service config` calls `rdd service start` to make sure the control plane is running and accepting API requests.

    * `rdd service start` checks `$APPDATA/rancher-desktop-2/rdd.pid` to see if the daemon is already running.

    * It finds that `$APPDATA/rancher-desktop-2` does not contain the `rdd.sqlite3` data store and calls `rdd service create`.

      * `rdd service create` will create the data store and a `config.json` with the default settings. It will also create `~/.rd2/bin` and extract bundled utilities into it.

    * `rdd service start` launches `rdd service serve` to run the control plane.

      * `rdd service serve` backgrounds itself (e.g. fork & exec) and writes `rdd.pid` to indicate that it is running.

      * It starts the apiserver and the controller-manager, which start the builtin controllers that have been specified in `config.json`. This will also create the certs and service account required to authenticate to the apiserver.

      * It copies the configuration into a config map in the `rdd-system` namespace.

    * `rdd service start` waits until `rdd.pid` exists. Then it tries to connect to the apiserver and authenticate until it succeeds.

  * `rdd service config` returns a kube config file that can be used to talk to the control plane.

* `rdd start` uses this kube config to fetch the `config` ConfigMap from the `rdd-system` namespace.

* It checks that the `app` controller has been configured.

* It creates the `rancher-desktop` namespace because it does not yet exist.

* It attempts to fetch the `App` object in the `rancher-desktop` namespace. This fails because the object does not exist yet.

* It creates an `App` instance with default settings and `spec.status: running`. It subscribes to the `status` of the new object.

  * The `App` controller creates a `template` (maybe just a config map) with the `lima.yaml` corresponding to the app settings.

  * It creates a `LimaVM` object referencing this template, also with `spec.status: running`, and subscribes to status updates of the object. The name of the VM is hardcoded to `rd`. This means Lima will create it in the `~/.rd2/lima/rd` directory.

    * The `LimaVM` controller creates the VM. Since the status is set to `running` it also starts the VM. Once the VM has completed startup the controller sets the status to "ready".

  * The `App` controller is notified of the status change of the `LimaVM` object and detects that the VM is running. It now monitors the VM to check when the `docker.sock` is accepting requests.

  * It creates a `rancher-desktop-2` docker context. If there is no current context, or if the context isn't accepting connections, then it makes the new context the default.

  * It sets the status of the `App` object to `ready`.

* `rdd start` is notified that the `App` status has changed. It verifies that the status is "ready" and exits.

Except for the backgrounding of the control plane process all other actions are performed in-process and don't spawn subprocesses.

## PATH and docker/kube context settings

### Setting up `PATH` and shell completions

`rdd start` has installed additional utilities to `~/.rd2/bin`, but now that directory must be added to the `PATH` as well:

```bash
export PATH="$HOME/.rd2/bin:$PATH
```

An alternative is run use [rdd shell-profile](cmd_app.md#rdd-shell-profile); it can also configure shell completions for `rdd` and the additional utilities.

```bash
source <(rdd shell-profile bash --path --completions)
```

This is the command that is being added by path-management to `~/.bash_profile`.

### Docker Context

`rdd start` has created the `rancher-desktop-2` docker context, but it will only activate it if the current context doesn't exist, or is non-functional.

The user will have to switch contexts explicitly if there is a possibility that another working context is already selected:

```bash
docker context use rancher-desktop-2
docker run hello-world
```

### Kubernetes Context

If Kubernetes has been enabled, e.g. with `rdd start --kube-version 1.33.2`, then a `rancher-desktop-2` kube context is created in `~/.rd2/kube.config`. If path-management is enabled, then the context will also be merged into `~/.kube/config` and made the current context there[^kubeconfig].

[^kubeconfig]: Setting up a list (like `KUBECONFIG=~/.kube/config:~/.rd2/kube.config` is not practical because the environment variable would need to be updated each time an RDD instance is created or removed.

### `rdd run`

An alternative to setting up the `PATH` and contexts is to use the [rdd run](cmd_app.md#rdd-run) command. It will setup the path and contexts just for a single command and does not modify the current configuration:

```bash
rdd run docker run hello-world
```
