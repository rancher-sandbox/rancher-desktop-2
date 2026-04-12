# Rancher Desktop Containers API

> [!CAUTION]
> The Rancher Desktop Containers API is still in the concept stage and the details
will need to be ironed out.

The Rancher Desktop Containers API mirrors the container engine state into
Kubernetes resources.  The engine controller connects to the container engine,
performs a full sync of containers, images, and volumes, then watches the engine
event stream for live updates.

All objects are in the `containers.rancherdesktop.io` API group.

All times are in RFC3339 format, per usual Kubernetes conventions.

When running `containerd`, the containerd namespace is listed as the `namespace`
label rather than re-using the Kubernetes namespace.  When running `dockerd`,
namespaces are not supported and we always use `moby` as the value for that label.

For the `*Request` resources, they use the `Complete` and `Failed` conditions to
express state; those are mutually exclusive (only one of the two can be set to
`True` at once).  Once either is set to `True`, the request object is considered
to be in a terminal state and will be removed after some timeout.  This will be
at least one minute (so the caller can read any data), but the precise timing is
unspecified.

This API is mainly for use by the Rancher Desktop front end; all other users are
strongly urged to use the relevant CLI or other API instead.

## Engine Mirroring

The engine controller (`pkg/controllers/app/engine/`) watches the `App` resource
for the `Running` condition.  When the VM is running with the `moby` backend,
the controller:

1. Connects to the Docker engine via the host socket.
2. Creates the `rancher-desktop` K8s namespace and the `moby`
   `ContainerNamespace` resource.
3. Lists all containers, images, and volumes and creates corresponding K8s
   resources.
4. Watches the Docker event stream for create, update, and delete events.

Containerd mirroring is not implemented yet; with `containerEngine.name=containerd`
the controller sets `ContainerEngineReady` to `True` with reason `NotApplicable`
and takes no mirroring action.

The controller sets the `ContainerEngineReady` condition on the `App` resource
to `True` after the initial sync completes.  Scripts can wait for readiness:

```sh
rdd ctl wait --for=condition=ContainerEngineReady=True app/app
```

When the VM shuts down or the container engine becomes unreachable, the
controller removes all mirror resources and sets `ContainerEngineReady` to
`False`.

### Finalizer lifecycle

Each mirror resource carries the `engine.rancherdesktop.io/docker-mirror`
finalizer.  When a user deletes a resource through the K8s API, the finalizer
handler deletes the corresponding object in the container engine, then removes
the finalizer to allow K8s garbage collection.

When the container engine deletes an object (the user ran `docker rm` or
`nerdctl rm`, for example), the engine controller strips the finalizer and
deletes the K8s resource directly, so no reverse engine call occurs.

## Namespaces

`ContainerNamespace` objects reflect the container engine namespaces.  This is
only useful when using the `containerd` backend; when using `dockerd`, the only
valid instance will have a name of `moby`, and it cannot be modified in any way.

`ContainerNamespace` objects only have the default metadata, since they
currently do not need anything else.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerNamespace
metadata:
  name: k8s.io # `moby` when using the dockerd engine.
  namespace: rancher-desktop
```

## Containers

`Container` objects reflect the running containers.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3 # container ID
  namespace: rancher-desktop
spec:
  state: unknown # Desired state (see below)
status:
  name: magical_gates
  namespace: k8s.io # Refers to a `ContainerNamespace` object
  path: /bin/sh
  args: [-c, 'sleep inf']
  # Image ID; corresponds to `Image` object's `.status.id` field.
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
  status: running
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

### Container state transitions

The engine controller creates all containers with `spec.state: unknown`.  This
means the engine mirrors container state without expressing intent; the
controller takes no start/stop action.

To control a container through the K8s API, set `spec.state` to `running` or
`created`.  The engine controller starts or stops the container accordingly.
The `status.status` field always reflects the actual container engine state.

Valid values for `spec.state`:
- `unknown` — engine-managed; no start/stop action taken (default)
- `running` — the container should be running
- `created` — the container should be stopped

### Container actions

#### Create container

To create a container, create a `ContainerCreateRequest` object:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerCreateRequest
metadata:
  name: whatever-12345
  namespace: rancher-desktop
spec:
  name: magical_gates # If unset, generate one randomly
  namespace: k8s.io # Refers to a `ContainerNamespace` object
  state: running # Default to `running`
  path: /bin/sh # defaults to image entry point / command
  args: [-c, 'sleep inf'] # defaults to image entry point / command
  image: "sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b" # accepts image tag
  ports: # merged with image defaults
    - name: 80/tcp
      bindings:
      - hostIP: 0.0.0.0
        hostPort: 32768
      - hostIP: '::'
        hostPort: 32768
  labels: # merged with image labels
    org.opensuse.base.vendor: openSUSE Project
status:
  # Resulting .metadata.name, which is the container ID.  It must be in the
  # same Kubernetes namespace as the ContainerCreateRequest.
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
  conditions:
  - type: Complete
    status: True
  - type: Failed
    status: False
```

If `.spec.namespace` / `.spec.name` duplicates an existing container, a
`CreateFailed` status is set with some details.

An admission controller will ensure that we cannot have multiple
`ContainerRequest` objects at the same time for the same containerd
`name`/`namespace` pair.

#### Change container state
Set `.spec.state` to `running` or `created`; the engine controller starts or
stops the container.

#### Fetch container logs
An endpoint at `/passthrough/containers/logs` will speak WebSocket; messages are
one way, text only, with each line being one message.

#### Exec (shell) in container
An endpoint at `/passthrough/containers/exec` will speak WebSocket; messages are
bidirectional, tentatively text only.

#### Delete container
Delete the `Container` object; a finalizer will be used to delete the container,
at which point the `Container` object will actually be deleted.

## Images

`Image` objects reflect images in the container engine.  Each tag is represented
as a new `Image` object; therefore, there may be multiple `Image` objects for
the same image ID (one per tag).  If an image without any tags exists, that will
be represented by an `Image` object without `.status.repoTag` and
`.status.namespace`.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Image
metadata:
  # Image ID, colon replaced with dot, with random suffix.
  name: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b-12345'
  namespace: rancher-desktop # not the containerd namespace
status:
  namespace: moby # Refers to a `ContainerNamespace` object
  # Image ID, in the raw form.
  id: 'sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b'
  repoDigests:
  - registry.opensuse.org/opensuse/leap@sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b
  # repoTag is unset if the image is not tagged
  repoTag: 'registry.opensuse.org/opensuse/leap:latest'
  createdAt: "2025-11-17T03:14:16Z"
  architecture: arm64
  os: linux
  size: 45150437
  labels:
    org.opensuse.base.vendor: openSUSE Project
  conditions: []
```

### Image Actions

#### Pull image
Create an `ImagePullRequest` object:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImagePullRequest
metadata:
  name: image-fetch-12345
  namespace: rancher-desktop
spec:
  namespace: moby # Refers to a `ContainerNamespace` object
  repoTag: 'registry.opensuse.org/opensuse/leap:latest'
status:
  conditions:
  - type: Complete
    status: True
  - type: Failed
    status: False
```

#### Build image
Not sure; do something with the `Resource` API maybe?

We may need an `ImageBuildRequest` job-thing or something?

#### Push image
Create an `ImagePushRequest` object; it will be removed some time after the push
has completed:
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImagePushRequest
metadata:
  name: image-push-12345
  namespace: rancher-desktop
spec:
  # `.metadata.name` of the image tag to push.
  imageRef: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b-12345'
status:
  conditions:
  - type: Complete
    status: True
  - type: Failed
    status: False
```

#### Scan image
We will need a new object type for this; maybe something like
```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImageScanRequest
metadata:
  name: image-scan-12345
  namespace: rancher-desktop # not containerd namespace
spec:
  # The `.metadata.name` of an `Image` object.
  imageRef: 'sha256.999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b-12345'
status:
  conditions:
  - type: Complete
    status: True
  - type: Failed
    status: False
  result:
    # Just dump the raw Trivy result JSON here (without converting to YAML).
    '{ ... }'
```

#### Untag image
Delete the `Image` object through the K8s API; the finalizer handler runs
`ImageRemove` on the corresponding Docker image reference.  Docker keeps the
underlying image as long as another tag or a running container references it,
so removing one tag does not necessarily delete the image itself.

The engine controller mirrors Docker untag events in the reverse direction:
when Docker emits an `untag` event, the controller re-inspects the image and
removes any K8s `Image` resources whose `.status.repoTag` is no longer present
in Docker's current tag list.  If the untag leaves the image dangling, a new
K8s `Image` object (without `.status.repoTag`) is created to represent it.

#### Delete untagged image
Delete the `Image` object (which does not have any `.status.repoTag` set).  An
admission controller must be set up so that this is not allowed if there is a
running container that uses that image.

## Volumes

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata:
  # `vol-` followed by the hex SHA-256 of the original Docker volume
  # name. Docker permits characters (uppercase, underscore) that are
  # invalid in RFC 1123 subdomains, so the controller hashes the name
  # and stores the original in `.status.name`.
  name: vol-d404559327842434dee6f7a10d8998594be5b49a7ef9a91a42ca2b3d0174ab9d
  namespace: rancher-desktop
status:
  name: volume-name
  namespace: k8s.io # Refers to a `ContainerNamespace` object
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
spec:
  name: volume-name
  namespace: k8s.io # Refers to a `ContainerNamespace` object
  driver: local
status:
  conditions:
  - type: Complete
    status: True
  - type: Failed
    status: False
```
Only local volumes are supported initially.
The `.spec` is expected to expand in the future, as more options are supported.

#### Delete volume
Delete the `Volume` object; finalizers will cause deletion of the container
engine side volume.
Webhooks will be needed for validation to reject deleting volumes that are in
use.
