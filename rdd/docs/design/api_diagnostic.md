# Diagnostic API

Diagnostics are similar to events: they are not managed by a single controller, but can be created and updated by any controller.

A diagnostic represents a single warning or error condition. The diagnostic is either raised or clear.

The GUI can simply subscribe to all `Diagnostic` objects in both the `rdd-system` and `rancher-desktop` namespaces.

## Sample diagnostic

```yaml
apiVersion: "rdd.rancherdesktop.io/v1alpha1"
kind: Diagnostic
metadata:
  name: docker-context
  group: "app.rancherdesktop.io"
spec:
  condition: "The docker context is not set to `rancher-desktop-2`"
  details: ""
  nextCheck: ""
  muted: false
  checkInterval: 5m
status:
  status: "Clear"
  lastCheck: ""
  lastRaised: ""
  lastClear: ""
```

The `metadata` uniquely identify a single diagnostic. The `group` is used so `name` doesn't have to be globally unique.

## Where is the muted status stored?

There may be diagnostics that are "errors". They don't exist in a cleared state, but will only be created when the error condition occurs, and will be deleted again when the owning object is deleted (or when the controller restarts).

All diagnostic objects should be deleted on controller restart, so the muted state cannot be stored with the diagnostic object itself.
