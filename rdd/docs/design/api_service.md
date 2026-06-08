# Service Lifecycle

## Service startup

(This section is missing information about setting up certificates and service accounts).

Unless otherwise specified, all objects mentioned here will be created in the `rdd-system` namespace.

* The control plane starts up the apiserver and the controller-manager with just the core controllers (Namespace, ConfigMap, ServiceAccount, RBAC).

* It creates the `rdd-system` namespace if it doesn't already exist.

* It creates a `config` ConfigMap and with the `config.json` data.

* It deletes all ConfigMaps with the annotation `shutdown.rancherdesktop.io/notification=true` (see [service-shutdown](#service-shutdown) below).

* It starts any additional controllers configured via the `config` ConfigMap.

## Service shutdown

The control plane is notified to shut down, either by `rdd service stop`
(SIGINT on Unix, `CTRL_BREAK_EVENT` on Windows; if the wait deadline expires
the wait force-terminates with SIGTERM on Unix or `TerminateProcess` on
Windows), or by the OS preparing to shut down the host.

Some controllers need to be notified of pending shutdown so they can gracefully terminate, e.g. shut down virtual machines.

Any controller can request shutdown notification by creating a ConfigMap in the `rdd-system` namespace:

```yaml
apiVersion: service.rancherdesktop.io/v1alpha1
kind: ConfigMap
metadata:
  name: shutdown-lima-controller
  namespace: rdd-system
  labels:
    shutdown.rancherdesktop.io/notification: "true"
data: {}
```

It subscribes to the metadata of this ConfigMap to receive a shutdown notification.

When the controller-manager receives the shutdown signal it will select all the ConfigMaps with this label and add a shutdown=pending annotation.

```shell
rdd ctl annotate configmap -n rdd-system \
    -l shutdown.rancherdesktop.io/notification=true \
    shutdown.rancherdesktop.io/shutdown=pending \
    --overwrite
```

The controller-manager subscribes to all these ConfigMaps. The individual controllers can now shut down their resources. Once done they change the annotation status from `pending` to `complete`.

Once the controller-manager has noticed that all shutdown objects are marked as `complete` the control plane will exit.
