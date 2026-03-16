# Controller Framework

The `pkg/controllers/base` package provides shared utilities for RDD controllers: registration, finalizers, owned-resource cleanup, and webhook helpers.

## Controller Registration

See also [controller manager discovery](discovery.md) for how multiple controller managers find each other at runtime.

Each controller implements `base.Controller` and registers itself via `init()`:

```go
func init() {
    base.RegisterController(&controller{})
}
```

`base.SharedControllerManager` discovers and starts all registered controllers.

## Finalizers

RDD uses two finalizers. Each serves a distinct purpose:

### `rdd.rancherdesktop.io/cleanup` (Self-Protection)

A resource's own controller sets this finalizer to run cleanup before deletion. For example, the LimaVM controller stops the VM and deletes disk files before removing the finalizer.

| Function | Purpose |
|----------|---------|
| `EnsureCleanupFinalizer` | Add cleanup finalizer to the resource |
| `RemoveCleanupFinalizer` | Remove cleanup finalizer after cleanup completes |
| `HasCleanupFinalizer` | Check whether the cleanup finalizer is present |

`DeleteOwnedResources` does **not** strip this finalizer. Only the resource's own controller removes it.

### `rdd.rancherdesktop.io/owned` (Cascade Blocking)

An owner controller sets this on child resources to block their deletion until the owner explicitly releases them via `DeleteOwnedResources`.

| Function | Purpose |
|----------|---------|
| `EnsureOwnedFinalizer` | Add owned finalizer to a child resource |
| `RemoveOwnedFinalizer` | Remove owned finalizer from a child resource |
| `HasOwnedFinalizer` | Check whether the owned finalizer is present |

### Why Two Finalizers?

A resource can need both finalizers. Consider a LimaVM owned by the App controller. The App controller sets `/owned` on the LimaVM so `DeleteOwnedResources` can release it during App deletion. The LimaVM controller sets `/cleanup` on itself to stop the VM and delete disk files before the resource disappears. With a single finalizer, `DeleteOwnedResources` would strip it, and the LimaVM would be deleted without ever running its cleanup — leaking the VM on disk.

## DeleteOwnedResources

`DeleteOwnedResources` finds all resources owned by a given object (via owner references), strips their `/owned` finalizer, and deletes them. It uses dynamic resource discovery to find all namespaced resource types without a hardcoded list.

For cluster-scoped owners, the resource must implement `ResourceNamespace` to specify which namespace contains its children.

## Owner References

RDD uses standard Kubernetes owner references (`metav1.OwnerReference`) to track parent-child relationships. `DeleteOwnedResources` checks `ownerReferences[].uid` to identify children. Controllers typically set owner references via `controllerutil.SetControllerReference`.

## Webhooks

Controllers that need admission webhooks implement the `base.WebhookController` interface:

```go
type WebhookController interface {
    SetWebhookPort(port int)
    GetWebhookServiceName() string
    GetWebhookManagers() []WebhookManager
}
```

`SharedControllerManager` calls `SetWebhookPort` with an allocated port before registration. The controller stores this port and uses it when building its `WebhookConfig`.

### WebhookConfig

`WebhookConfig[T]` describes a single webhook. Provide a `Validator`, a `Defaulter`, or both:

| Field | Default | Purpose |
|-------|---------|---------|
| `Name` | — | Kubernetes resource name for the webhook configuration |
| `WebhookName` | — | Hook name within the configuration (FQDN convention) |
| `WebhookPort` | — | Port from `SetWebhookPort` |
| `Operations` | Create, Update | Which admission operations trigger the webhook |
| `FailurePolicy` | Fail | How the API server handles webhook failures |
| `SideEffects` | None | Whether the webhook has side effects |
| `ObjectSelector` | nil | Label selector to filter which objects reach the webhook |
| `Validator` | nil | Validating admission handler |
| `Defaulter` | nil | Mutating admission handler |

### Setup Flow

Registration happens in two phases:

1. **During `RegisterWithManager`**, the controller calls `SetupWebhookForResource` for each webhook. This registers handlers with controller-runtime and returns `WebhookManager` instances (one per webhook type — validating and/or mutating). The controller appends these to an internal slice that `GetWebhookManagers()` exposes.

2. **After registration**, `SharedControllerManager` calls `Setup()` on each `WebhookManager` in parallel. This creates the actual `ValidatingWebhookConfiguration` or `MutatingWebhookConfiguration` resources in the API server, with retry logic for transient failures.

### Certificate Management

`SharedWebhookCertificateManager` generates self-signed certificates for webhook TLS:

- **CA certificate** (10-year validity) is persisted and reused across restarts.
- **Server certificate** (1-year validity) is regenerated on each startup. DNS SANs cover all service name variations (e.g., `limavm-webhook`, `limavm-webhook.default`, `limavm-webhook.default.svc.cluster.local`).
- Certificates regenerate automatically when a new controller adds service names or when the server cert has fewer than 30 days remaining.

The CA bundle is injected into each webhook configuration so the API server can verify the webhook's TLS certificate.

### Helpers

- `base.IsDryRun(ctx)` extracts the admission request from context and returns true if the request is a dry run. Controllers use this to skip side effects (e.g., creating child resources) during dry-run admission.
