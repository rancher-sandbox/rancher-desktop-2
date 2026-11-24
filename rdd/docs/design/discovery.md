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

A controller manager patches the ConfigMap on startup (adding its API group key) and removes the key on shutdown. The ConfigMap itself is owned by the control plane: only the control plane creates or replaces it. The control plane recreates it on startup, dropping any stale entries from a previous crash. Shutdown does not delete the ConfigMap; while the daemon is still serving, concurrent clients can continue to use the last published discovery data, and the next startup is responsible for clearing stale state.

## Ready annotation

The annotation `rdd.rancherdesktop.io/ready=true` signals that every enabled controller has established its CRDs **and** registered its data entry in the ConfigMap, that its admission webhooks are configured and accept connections, and that the passthrough HTTP server is ready. Clients that depend on CRDs (for example, `rdd set`, which fetches the App CRD schema) or on the enabled-controller list (for example, `rdd set running=true`, which checks whether the engine controller is present) must wait for the annotation; otherwise they race startup and see either `the server could not find the requested resource` or a stale, empty controller list.

The control plane sets the annotation in one of two places:

1. Immediately after creating the ConfigMap, if no controllers are enabled.
2. Otherwise, inside the shared controller manager's startup goroutine, after `installControllerCRDs` has established every CRD, `registerDiscovery` has written the controller-manager entry into `configMap.Data`, the webhook configurations are applied, the webhook server accepts connections, and the passthrough HTTP server is ready.

Ordering every step before the annotation keeps the "ready = clients may proceed" contract consistent. Any client that waits for the annotation sees the CRDs, the enabled-controller list, and working admission webhooks.

The ConfigMap is recreated on every control plane startup, so a stale `ready` from a previous run is cleared before CRD installation begins. But a stopped control plane leaves its ConfigMap in place, annotation included, and the control plane replaces it only once the API server is ready. Until then a client sees the previous run's `ready`, so it must also check that the ConfigMap's `creationTimestamp` is at or after the control plane's start time.

The annotation only covers controllers known at control plane startup. External controllers (for example, the mock controller, or a controller manager started later via `rdd svc create --controllers=...`) can attach at any time, so a client waiting for the ready annotation must not assume that every controller it cares about is already registered. Discover external controllers by reading the ConfigMap directly.

## Consumers

**Service readiness** -- The CLI waits until the ConfigMap has the ready annotation and a `creationTimestamp` at or after the control plane's start time, as described under [Ready annotation](#ready-annotation).

**Passthrough proxy** -- The control plane exposes a `/passthrough/<controller>/<endpoint>/...` HTTP route that looks up the target manager via the ConfigMap and reverse-proxies the request.

**External controller startup** -- An external controller checks the ConfigMap to see whether its controllers are already running (to avoid conflicts) and monitors it to detect when the control plane has stopped.

**Health gating** -- `IsControllerRunning` performs an HTTP health check against the registered `healthEndpoint` before reporting a controller as running.
