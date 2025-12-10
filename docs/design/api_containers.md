# Rancher Desktop Containers API

> [!CAUTION]
> The Rancher Desktop Containers API is still in the concept stage and the details
will need to be ironed out.

The Rancher Desktop Containers API is a mostly read-only reflection of the
running container engine objects; unless otherwise noted, any modification will
be rejected by the controllers.

All objects are in the `containers.rancherdesktop.io` API group.

This is mainly for use by the Rancher Desktop front end; all other users are
strongly urged to use the relevant CLI or other API instead.

## Containers

`Container` objects reflect the running containers.  If the container engine
supports namespaces, then they are in the namespace with the same name;
otherwise, all containers are in the `default` namespace.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3 # container ID
  namespace: default # matches containerd namespace, or `default` if not supported
  labels:
    name: magical_gates
spec:
  path: /bin/sh
  args: [-c, 'sleep inf']
  image: "sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b"
  ports:
    - name: 80/tcp
      bindings:
      - hostIp: 0.0.0.0
        hostPort: 32768
      - hostIp: '::'
        hostPort: 32768
  labels:
    org.openbuildservice.disturl: obs://build.opensuse.org/openSUSE:Leap:15.6:Images/images/54f24e8a33253ce205ee46962e0db0c8-opensuse-leap-image:docker
status:
  status: Running
  pid: 5059
  exitCode: 0
  error: ""
  createdAt: "2025-11-22T00:34:07.153640108Z" # Time
  startedAt: "2025-12-09T22:05:27.774478174Z"
  finishedAt: "2025-11-29T00:35:49.155454569Z"
  conditions:
  - type: Running
    status: True
  - type: Paused
    status: False
  - type: Restarting
    status: False
  - type: OOMKilled
    status: False
  - type: Dead
    status: False
```

## Images

`Image` objects reflect images in the container engine.  If the container engine
supports namespaces, then they are in the namespace with the same name;
otherwise, all images are in the `default` namespace.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Image
metadata:
  name: 'sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b' # Image ID
  namespace: default # matches containerd namespace, or `default` if not supported
spec:
  repoTags:
  - registry.opensuse.org/opensuse/leap:latest
  repoDigests:
  - registry.opensuse.org/opensuse/leap@sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b
  createdAt: "2025-11-17T03:14:16Z"
  architecture: arm64
  os: linux
  size: 45150437
  labels:
    org.openbuildservice.disturl: obs://build.opensuse.org/openSUSE:Leap:15.6:Images/images/54f24e8a33253ce205ee46962e0db0c8-opensuse-leap-image:docker
```

## Volumes

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata:
  name: volume-name
  namespace: default # matches containerd namespace, or `default` if not supported
spec:
  createdAt: "2025-11-17T03:14:16Z"
  driver: local
  mountpoint: /var/lib/docker/volumes/volume-name/_data
  labels: {}
  scope: local
  options: {}
```
