# Rancher Desktop Containers API

> [!CAUTION]
> The Rancher Desktop Containers API is still in the concept stage and the details
will need to be ironed out.

The Rancher Desktop Containers API is a mostly read-only reflection of the
running container engine objects; unless otherwise noted, any modification will
be rejected by the controllers.

All objects are in the `containers.rancherdesktop.io` API group.

When running `containerd`, the containerd namespace is listed as the `namespace`
label rather than re-using the Kubernetes namespace.  When running `dockerd`,
namespaces are not supported and we always use `moby` as the value for that label.

This is mainly for use by the Rancher Desktop front end; all other users are
strongly urged to use the relevant CLI or other API instead.

## Containers

`Container` objects reflect the running containers.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3 # container ID
  namespace: default
  labels:
    name: magical_gates
    namespace: k8s.io # containerd namespace, or `moby` if using dockerd
spec:
  state: running # Desired state
status:
  path: /bin/sh
  args: [-c, 'sleep inf']
  image: "sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b"
  ports:
    - name: 80/tcp
      bindings:
      - hostIP: 0.0.0.0
        hostPort: 32768
      - hostIP: '::'
        hostPort: 32768
  labels:
    org.opensuse.base.vendor: openSUSE Project
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

Deleting a `Container` object causes the finalizer to delete the matching
container in the container engine.

### Container actions

We will need to support a variety of actions on containers:

#### Create container

To create a container, create a `ContainerCreateRequest` object:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerCreateRequest
metadata:
  name: whatever-12345
  namespace: rancher-desktop
  labels:
    name: magical_gates # If unset, generate one randomly
    namespace: k8s.io # containerd namespace; defaults to `default` / `moby`.
spec:
  state: running # Default to `running`
  path: /bin/sh # defaults to image entry point / command
  args: [-c, 'sleep inf'] # defaults to image entry point / command
  image: "sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b" # accepts image tag
  ports: # merged with image defaults
    - name: 80/tcp
      bindings:
      - hostIp: 0.0.0.0
        hostPort: 32768
      - hostIp: '::'
        hostPort: 32768
  labels: # merged with image labels
    org.opensuse.base.vendor: openSUSE Project
  state: running # initial desired state, either `running` or `created`; defaults to `running`.
status:
  # Resulting .metadata.name, which is the container ID.  It must be in the
  # same Kubernetes namespace as the ContainerCreateRequest.
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
  conditions:
  - type: Finished
    status: True
```

The `ContainerCreateRequest` will be deleted soon after the `Finished` condition
transitions to `True`.

If `.metadata.labels.namespace` / `.metadata.labels.name` duplicates an existing
container, an `CreateFailed` status is set with some details.

If multiple `ContainerRequest` objects exist at the same time for the same
contained `name`/`namespace` pair, the result is undefined.

#### Change container state
Set `.spec.state` to match how Kubernetes resources normally work.

#### Fetch container logs
The `@kubernetes/client-node` package has some hand-written code to deal with
logs; maybe we can make a copy of that (with a different endpoint) for this?

#### Exec (shell) in container
Same as logs; there's some special code in `@kubernetes/client-node` that we may
be able to fork.

#### Delete container
Delete the `Container` object; a finalizer will be used to delete the container,
at which point the `Container` object will actually be deleted.

## Images

`Image` objects reflect images in the container engine.  Each tag is represented
as a new `Image` object (therefore there may be duplication).

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Image
metadata:
  name: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b-12345' # Image ID, colon replaced with dot, with random suffix.
  namespace: rancher-desktop # not the containerd namespace
  labels:
    # If an image is untagged, `name` and `namespace` are missing.
    name: 'registry.opensuse.org/opensuse/leap:latest' # image tag
    id: 'sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b' # image ID
    namespace: moby # containerd namespace
spec:
  push: false
status:
  repoDigests:
  - registry.opensuse.org/opensuse/leap@sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b
  createdAt: "2025-11-17T03:14:16Z"
  architecture: arm64
  os: linux
  size: 45150437
  labels:
    org.opensuse.base.vendor: openSUSE Project
  conditions:
  - type: PullStarted
    status: True
  - type: Ready
    status: True
  - type: Pushed
    status: False
```

### Image Actions

#### Fetch image
Create an `ImagePullRequest` object:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImagePullRequest
metadata:
  name: image-fetch-12345
spec:
  repoTag: 'registry.opensuse.org/opensuse/leap:latest'
status:
  - type: Pulled
    status: False
```

#### Build image
Not sure; do something with the `Resource` API maybe?

We may need an `ImageBuildRequest` job-thing or something?

#### Push image
Set `.spec.push` to `true`.  The image will be pushed by `.metadata.labels.name`.
After the push, `.status[?(@.type=="Pushed")]` will be set to `True`, and
`.spec.push` will be set to `false`.

#### Scan image
We will need a new object type for this; maybe something like
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImageScanRequest
metadata:
  name: image-scan-12345
  namespace: rdd-system # not containerd namespace
spec:
  # The `.metadata.name` of an `Image` object.
  imageRef: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b-12345'
status:
  conditions:
  - type: Finished
    status: True
  result:
    # Just dump the Trivy result JSON here (without converting to YAML).
```

#### Untag image
Delete the `Image` object; the finalizer will untag the image.  If all tags of
an image are removed, _and_ it is not in use by a container (running or not),
then the image itself is removed.

## Volumes

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata:
  name: volume-name-12345 # based on containerd name / namespace?
  namespace: default # Not related to containerd namespace
  labels:
    name: volume-name
    namespace: k8s.io # containerd namespace, or `moby` if using dockerd
spec:
  createdAt: "2025-11-17T03:14:16Z"
  driver: local
  mountpoint: /var/lib/docker/volumes/volume-name/_data
  labels: {}
  scope: local
  options: {}
```

### Volume Actions

#### Create volume
Create a `VolumeCreateRequest`:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: VolumeCreateRequest
metadata:
  name: volume-create-12345
  namespace: default
  labels:
    name: volume-name
    namespace: k8s.io # containerd namespace, or `moby` if using dockerd
spec: # same as Volume.spec
status:
  conditions:
  - type: Processed
    status: False
```
Only local volumes are supported initially.
Parts of `.spec` may be ignored.
The request is removed some time after the volume has been created.

#### Delete volume
Delete the `Volume` object; finalizers will cause deletion of the container
engine side volume.
Webhooks will be needed for validation to reject deleting volumes that are in
use.
