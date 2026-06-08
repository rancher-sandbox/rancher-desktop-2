# k3s version controller

The `K3sVersions` object is part of the `app.rancherdesktop.io` API.

It maintains an up-to-date list of `k3s` versions and generates [`Resource`](api_resource.md) objects for each version.

## `K3sVersions`

The `K3sVersion` object is used to tell the controller to maintain a `k3s` version list in the same namespace. There will normally just be a single instance created by the `App` singleton.

```yaml
apiVersion: app.rancherdesktop.io/v1alpha1
kind: K3sVersions
metadata:
  name: k3s-versions
  namespace: rancher-desktop
spec:
  repo: k3s-io/k3s
  channel: https://update.k3s.io/v1-release/channels
  minVersion: 1.25.0
status:
  lastCheck: "2025-06-09T04:55:00Z"
  lastStatus: 200
```

### URL monitors

The controller will create two `URLMonitor` objects to watch the update channel and the GitHub releases pages. It will be notified when either one changes.

When a change is detected, the `k3s` version list is constructed and a `Resource` object is created for each version that doesn't have one yet. The `latest` and `stable` state will be updated by applying labels to the `Resource` objects.

### Resource objects

The `Resource` objects for each `k3s` version will include the download URL, so the resource controller can fetch and cache the binary when a `Checkout` object is requesting it.

## GITHUB token???

XXX How are we going to deal with the need for a GitHub token? Should we just retry throttled requests?
