# Host info controller

The `HostInfo` object is part of the `rdd.rancherdesktop.io` API.

It publishes the hardware limits of the host — the logical CPU count and the total memory — so that clients such as the GUI can offer a valid range for the VM settings in [`App.spec.virtualMachine`](api_app.md) without inspecting the host themselves.

## `HostInfo`

`HostInfo` is cluster-scoped, and the controller maintains exactly one instance, named `system`. The spec is empty: the object is read-only to clients, and the controller owns its status.

```yaml
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: HostInfo
metadata:
  name: system
spec: {}
status:
  cpus: 10
  memory: 32Gi
```

- **status.cpus**: The number of logical CPUs on the host.
- **status.memory**: The total host memory, as a quantity, so a client can compare it against `App.spec.virtualMachine.memory` without converting units.

Both fields are always serialized. A zero reading means detection failed, which leaves the matching ceiling in the App admission webhook unenforced; an absent field would hide that from a client.

The controller creates the singleton when it starts and retries until it succeeds, then fills in the status on the reconcile that the create schedules. Deleting the object recreates it. A `HostInfo` under any other name is ignored.

## Relationship to App admission

The host is the source of truth, and two readers consult it independently: this controller, which publishes what it finds in `HostInfo.status`, and the App webhooks, which snapshot the same limits when they are set up. Neither reads the other. Both go through `pkg/hostinfo.Detect()`, so they cannot disagree about how the limits are computed — only about when they were read, since the webhook reads once at startup while the reconciler reads on every reconcile.

Issue #498 asked that admission use the saved values, so `HostInfo` would be the one place the limits are stored. It does not, which leaves `HostInfo` a resource that nothing inside the daemon reads: its only consumer is the GUI. The reason to keep it that way is that the `app` group then needs nothing from the `rdd` group — admission works under `--controllers=app` alone, where no `HostInfo` singleton exists at all.
