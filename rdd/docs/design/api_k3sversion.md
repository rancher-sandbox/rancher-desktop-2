# k3s version controller

The `K3sVersions` controller in the `app.rancherdesktop.io` API group manages
the list of valid K3s versions.  It generates a `ConfigMap` with information
about the supported versions and channels.

## `k3s-versions` `ConfigMap`

A `ConfigMap` named `k3s-versions` is maintained in the namespace named by the
[`App` object](api_app.md) (in `spec.namespace`).  The `ConfigMap` has two
pieces of data, both serialized as JSON:

- The `versions` key is a mapping of version (e.g. `1.32.4`) to the full k3s
  version number (`v1.32.4+k3s1`).
- The `channels` key is a mapping of channel name (e.g. `stable`, `1.32`) to the
  corresponding version, as a key into `versions`; for example, `1.32.4`.

The `ConfigMap` will be created as soon as the `App` object exists, and any
changes to the two keys above will be reverted.

### Example

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: k3s-versions
  namespace: rancher-desktop
data:
  channels: '{"1.32":"1.32.13","latest":"1.35.3","stable":"1.34.6"}'
  versions: '{"1.32.0":"v1.32.0+k3s1","1.32.1":"v1.32.1+k3s1"}'
```

Please note that the example has been modified for brevity.
