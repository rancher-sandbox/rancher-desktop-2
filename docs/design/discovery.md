# Controller Manager Discovery

See also the [controller framework](controllers.md) for base controller utilities (registration, finalizers, owned-resource cleanup).

Controller managers register themselves in a shared ConfigMap so that other components can find them. The control plane uses this to determine which controllers are running, to route passthrough requests, and to detect when an external controller manager has gone away.

## ConfigMap

The ConfigMap is named `rdd-controller-manager` in the `rdd-system` namespace. Each key in `data` is an API group name (e.g. `lima.rancherdesktop.io`). Each value is a JSON object:

```json
{
  "healthPort": 9081,
  "metricsPort": 9082,
  "enabledControllers": ["LimaVM", "LimaDisk"],
  "enabledPassthroughs": {
    "LimaVM": ["serial"]
  },
  "startTime": "2026-03-06T12:00:00Z",
  "healthEndpoint": "http://localhost:9081/healthz",
  "metricsEndpoint": "http://localhost:9082/metrics",
  "passthroughEndpoint": "http://localhost:9083/"
}
```

| Field                  | Description                                              |
|------------------------|----------------------------------------------------------|
| `healthPort`           | Port for the `/healthz` endpoint.                        |
| `metricsPort`          | Port for the `/metrics` endpoint.                        |
| `enabledControllers`   | Controllers this manager runs.                           |
| `enabledPassthroughs`  | Map of controller name to passthrough endpoint names.    |
| `startTime`            | UTC timestamp of when the manager registered.            |
| `healthEndpoint`       | Derived URL for health checks.                           |
| `metricsEndpoint`      | Derived URL for metrics scraping.                        |
| `passthroughEndpoint`  | Base URL for proxying passthrough requests.              |

## Lifecycle

The control plane creates the ConfigMap with an empty `data` map as soon as the API server is ready, before any controller manager starts. Its `creationTimestamp` therefore marks the control plane start time, which [`rdd ctl await --since=startup`](cmd_service.md) uses to filter condition transitions.

A controller manager patches the ConfigMap on startup (adding its API group key) and removes the key on shutdown. The ConfigMap itself is owned by the control plane: only the control plane creates or deletes it. The control plane recreates it on startup (dropping any stale entries from a previous crash) and deletes it on shutdown.

## Ready annotation

The annotation `rdd.rancherdesktop.io/ready=true` signals that every enabled controller has installed its CRDs **and** registered its data entry in the ConfigMap. Clients that depend on CRDs (for example, `rdd set`, which fetches the App CRD schema) or on the enabled-controller list (for example, `rdd set running=true`, which checks whether the engine controller is present) must wait for the annotation; otherwise they race startup and see either `the server could not find the requested resource` or a stale, empty controller list.

The control plane sets the annotation in one of two places:

1. Immediately after creating the ConfigMap, if no controllers are enabled.
2. Otherwise, inside the shared controller manager's startup goroutine, after `installControllerCRDs` has established every CRD **and** `registerDiscovery` has written the controller-manager entry into `configMap.Data`.

Ordering both steps before the annotation keeps the "ready = clients may proceed" contract consistent: any client that waits for the annotation sees both CRDs and the enabled-controller list, not just one of the two.

The ConfigMap is recreated on every control plane startup, so a stale `ready` from a previous crash is always cleared before CRD installation begins.

The annotation only covers controllers known at control plane startup. External controllers (for example, the mock controller, or a controller manager started later via `rdd svc create --controllers=...`) can attach at any time, so a client waiting for the ready annotation must not assume that every controller it cares about is already registered. Discover external controllers by reading the ConfigMap directly.

## Consumers

**Service readiness** -- The control plane queries the ConfigMap to discover which controllers are running across all managers. It uses this to decide when external controllers have finished registering.

**Passthrough proxy** -- The control plane exposes a `/passthrough/<controller>/<endpoint>/...` HTTP route that looks up the target manager via the ConfigMap and reverse-proxies the request.

**External controller startup** -- An external controller checks the ConfigMap to see whether its controllers are already running (to avoid conflicts) and monitors it to detect when the control plane has stopped.

**Health gating** -- `IsControllerRunning` performs an HTTP health check against the registered `healthEndpoint` before reporting a controller as running.
