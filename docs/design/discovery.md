# Controller Manager Discovery

See also the [controller framework](controllers.md) for base controller utilities (registration, finalizers, owned-resource cleanup).

Controller managers register themselves in a shared ConfigMap so that other components can find them. The control plane uses this to determine which controllers are running, to route passthrough requests, and to detect when an external controller manager has gone away.

## ConfigMap

The ConfigMap is named `rdd-controller-manager` in the `rdd-system` namespace. Each key is an API group name (e.g. `lima.rancherdesktop.io`). Each value is a JSON object:

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

A controller manager patches the ConfigMap on startup (adding its API group key) and removes the key on shutdown. The ConfigMap itself is deleted when the last key is removed.

The control plane also cleans up the entire ConfigMap during service shutdown and on startup (to clear stale entries from a previous crash).

## Consumers

**Service readiness** -- The control plane queries the ConfigMap to discover which controllers are running across all managers. It uses this to decide when external controllers have finished registering.

**Passthrough proxy** -- The control plane exposes a `/passthrough/<controller>/<endpoint>/...` HTTP route that looks up the target manager via the ConfigMap and reverse-proxies the request.

**External controller startup** -- An external controller checks the ConfigMap to see whether its controllers are already running (to avoid conflicts) and monitors it to detect when the control plane has stopped.

**Health gating** -- `IsControllerRunning` performs an HTTP health check against the registered `healthEndpoint` before reporting a controller as running.
