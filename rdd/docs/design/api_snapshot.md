# Snapshot API

**Note** This document is still at the hand-wavy/concept stage and not well thought through.

A snapshot records the current status, and can be restored in the future.

Snapshots can be restored into different contexts. E.g a VM snapshot may be restored using a different name, effectively creating a clone. A snapshot may even be restored into a different RDD instance. Or it can be part of an air-gap [package](package.md) that will be used on a different host.

## Snapshot operator

Snapshots are implemented using the operator pattern. Other object types (especially the `LimaVM`) may need to implement a mechanism to co-operate in saving/restoring a snapshot (by shutting down the VM, copying the data, and powering it back up).

## Snapshot scopes

Snapshots can have different scopes:

### App snapshot

Captures only the data disk and the app settings.

It may include non-downloadable resources.

#### Cross-platform

An App snapshot can be stored in cross-platform format (likely tarball) that can be restored on any supported platform.

### LimaVM snapshot

Captures a single `LimaVM` instance and all owned objects (LimaDisks, LimaNetworks, ConfigMaps, resource Checkouts).

Can be restored within the same RDD instance but needs to change the `LimaVM` instance name.

### Namespace snapshot

Captures everything within a namespace.

Maybe can be restored into a different namespace, but needs to deal with `LimaVM` instance name collisions.

### RDD instance snapshot

Everything and the kitchen sink.
