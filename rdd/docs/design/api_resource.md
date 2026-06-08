# Resource API

The resource controller manages all file resources for RDD. This includes downloading and caching resources from remote URLs.

Examples of resources managed are:

* VM images
* k3s tarballs
* external utilities

A `Resource` object specifies the name of the resource and tells the controller how to download it (or where to find it locally).

Consumers of the resource need to create a `Checkout` object for the `Resource` to get a local filepath to it. The controller may need to download the file first, or extract it from a local tarball, or find it in the cache etc.

`Resources` can verify checksums, signatures, attestations, etc. so a [deployment profile](profile.md) can specify which resources should be trusted.

## Resource objects

```yaml
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Resource
metadata:
  name: v1.33.1+k3s1
  kind: k3s
  group: app.rancherdesktop.io
  namespace: rancher-desktop
spec:
  url: https://github.com/k3s-io/k3s/releases/download/v1.33.1%2Bk3s1/k3s-airgap-images-amd64.tar.zst
status:
  status: cached
```

The combination of `namespace`, `name`, and `group` uniquely identifies a resource **globally**.
The `kind` is optional and only exists for discovery (e.g. "select all resource whose `kind` is `k3s`" to get a list of all `k3s` version for a GUI dialog).

## Checkout objects

```yaml
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Checkout
metadata:
  name: v1.33.1+k3s1
  group: app.rancherdesktop.io
  namespace: rancher-desktop
spec:
  readonly: true
status:
  progress: "Downloading xxx 28%"
  localPath: "/Users/xxx/Library/Caches/..."
```

To avoid additional local copies the `Checkout` object may suggest a destination for the checkout.

It is possible to request a "read-only" checkout, which on systems without CoW semantics may return the path to the file in the cache. It is the responsibility of the `Checkout` owner not to modify the resource.

The `App` controller may copy the `status.progress` of the `Checkout` object to the `status.progress` of the `App` object so that can be displayed in the GUI status bar.

## TBD

### Cache Management

Each control plane maintains its own cache. The cache manager however may look at the cached files of other RDD instances and "download" a file from that cache instead of the original URL (it still verifies the checksum). This "download" may just be CoW copy.

This means the cache data must be stored outside the control plane, because the instance may not be running.

### Mock support

The `Resource` object could have a `mockHTTPStatus: 404` field that will be used in place of actually attempting to download a URL.
