# Rancher Desktop Containers API

The Rancher Desktop Containers API mirrors the container engine state into
Kubernetes resources.  The engine controller connects to the container engine,
performs a full sync of containers, images, and volumes, then watches the engine
event stream for live updates.

All objects are in the `containers.rancherdesktop.io` API group.

All times are in RFC3339 format, per usual Kubernetes conventions.

## Terminology

Capitalized `Container`, `Image`, `Volume`, and `ContainerNamespace`
refer to the resource types in this API group. Lowercase `container`,
`image`, and `volume` refer to the underlying Docker engine objects.
The rdd LimaVM also runs k3s, so "k8s container" would be ambiguous —
this doc, code comments, and commit messages rely on capitalization
instead.

Where capitalization alone is ambiguous (sentence start, or prose that
mentions both the engine object and the resource), the resources are
called "Container mirrors", "Image mirrors", and "Volume mirrors", after
the engine controller's role. The code uses the same terminology: the
finalizer is `engine.rancherdesktop.io/mirror`, the cleanup
helper is `cleanupMirrorResources`, and the name helper is
`volumeMirrorName`.

When running `containerd`, the containerd namespace is listed as the `namespace`
label rather than re-using the Kubernetes namespace.  When running `dockerd`,
namespaces are not supported and we always use `moby` as the value for that label.

For the `*Request` resources, they use the `Settled` and `Failed` conditions to
express state.  `Settled` will become `True` when the request object has reached
a terminal state (it will not produce any more actions).  Once a request object
has reached such a terminal state, unless it has an owning reference set, it
will be automatically removed after some timeout.  This will be at least one
minute (so the caller can read any data), but the precise timing is unspecified.
If the `Settled` condition is set to `True` but it is not successful, then the
`Failed` condition will exist and set to `True`, with the reason and message
describing what the error is.

This API is mainly for use by the Rancher Desktop front end; all other users are
strongly urged to use the relevant CLI or other API instead.

## Engine Mirroring

The engine controller (`pkg/controllers/app/engine/`) watches the `App` resource
for the `Running` condition.  When the VM is running with the `moby` backend,
the controller:

1. Connects to the Docker engine via the host socket.
2. Creates the `moby` `ContainerNamespace` resource. The `rancher-desktop`
   Kubernetes namespace itself is the App controller's.
3. Lists all Docker containers, images, and volumes and creates the
   corresponding `Container`, `Image`, and `Volume` mirrors.
4. Watches the Docker event stream for create, update, and delete events.

With `containerEngine.name=containerd` the controller runs a containerd watcher
instead, mirroring each containerd namespace along with its containers and
images.  A namespace whose name is not a valid Kubernetes object name gets no
`ContainerNamespace` mirror; its containers are still mirrored.  Whether a
container's mirror name is hashed turns on its own ID, not on the namespace
holding it, so a container with an ordinary ID keeps that ID as its mirror
name in a skipped namespace exactly as it would anywhere else.  Its
`.status.namespace` then names a namespace no `ContainerNamespace` object
represents.  containerd has no volume concept, so it creates no `Volume`
mirrors.
Windows is the exception, since nothing serves the containerd named pipe there
yet; the controller sets `ContainerEngineReady` to `True` with reason
`NotApplicable` and takes no mirroring action.

The controller sets the `ContainerEngineReady` condition on the `App` resource
to `True` after the initial sync completes.  Scripts can wait for readiness:

```sh
rdd ctl wait --for=condition=ContainerEngineReady=True app/app
```

When the VM shuts down or the container engine becomes unreachable, the
controller removes all mirror resources and sets `ContainerEngineReady` to
`False`.

### Finalizer lifecycle

`Container`, `Image` and `Volume` mirrors carry the
`engine.rancherdesktop.io/mirror` finalizer. A K8s-side delete triggers
the finalizer handler, which deletes the corresponding engine object and
then strips the finalizer so the mirror can be garbage-collected.
`ContainerNamespace` mirrors carry no finalizer: deleting one is not
offered as a way to delete the engine namespace, so a finalizer with no
handler would only trap the delete in Terminating.

An engine-side delete (for example, `docker rm`) goes the other way:
the engine controller strips the finalizer and deletes the mirror
directly, without calling back into the engine.

### containerd state the controller does not maintain

On containerd the controller drives the engine over its API, while
`nerdctl` maintains state the controller does not write: the container
name reservation and its state directory, both of which live outside
containerd, and the `containerd.io/restart.*` container labels, which
live on the containerd record but are written only by `nerdctl` and by
containerd's own restart monitor.
Actions taken through the mirror leave all of it untouched, with two
consequences today.

Restart policies do not survive a mirror-driven stop. containerd's
restart monitor decides from `containerd.io/restart.explicitly-stopped`,
which `nerdctl stop` and `nerdctl kill` set and `nerdctl start` clears,
so stopping an `unless-stopped` container through the action annotation
lets the monitor start it again even though `status.lastAction` reports
success.

Deleting the mirror of a container that was created but never started
leaves its name reserved. `nerdctl` releases a name either from
`nerdctl rm` or from the container's post-stop hook, and a container
with no task has run neither, so `nerdctl create --name` rejects that
name afterwards.

## Namespaces

`ContainerNamespace` objects reflect the container engine namespaces.  This is
only useful when using the `containerd` backend; when using `dockerd`, the only
valid instance will have a name of `moby`, and it cannot be modified in any way.
There is currently no API defined to create them.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerNamespace
metadata:
  name: cns-c6142cdbbd05b7f761a73a0e6e74bcf2fa20e936985f036e0279faf6016f0104
  namespace: rancher-desktop
status:
  name: moby
  labels: {}
```

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: As container namespaces may not be valid Kubernetes object
  names, this is `cns-` followed by the lower case SHA-256 hash of the container
  namespace name.
- **status.name**: The container namespace.
- **status.labels**: Containerd labels for namespaces.  Does not apply to moby.

## Containers

`Container` objects reflect the running containers. The spec is empty:
the engine owns the observed state in `status`, and the user drives the
container through the action annotation described below.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
  namespace: rancher-desktop
  annotations:
    containers.rancherdesktop.io/action: pause
spec: {}
status:
  name: magical_gates
  namespace: k8s.io
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
  status: running
  pid: 5059
  exitCode: 0
  error: ""
  createdAt: "2025-11-22T00:34:07.153640108Z"
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
  lastAction:
    action: pause
    state: Succeeded
    observedAt: "2026-04-15T10:30:00Z"
    completedAt: "2026-04-15T10:30:00Z"
```

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: The container ID, in lower case hexidecimal.  This is
  always a valid Kubernetes object name.
- **metadata.annotations[containers.rancherdesktop.io/action]**: request a
  one-shot action; see [Container Actions](#container-actions) below.
- **status.name**: The container name.
- **status.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **status.path**: The path to the executable for PID 1 in the container.
- **status.args**: The arguments to the executable for PID 1 in the container.
- **status.image**: Image ID; corresponds to [`Image`](#images) object's
  `.status.id` field.
- **status.ports**: Exposed ports, similar to moby engine API.
- **status.labels**: Container labels.
- **status.status**: One of `created`, `running`, `pausing`, `restarting`,
  `removing`, `exited`, `dead`, or `unknown` (the default).
- **status.pid**: The pid of the container process.
- **status.exitCode**: Last exit code of the container.
- **status.error**: Error message when running the container.
- **status.createdAt**: Time when the container was created.
- **status.startedAt**: Time when the container started.
- **status.finishedAt**: Time when the container exited.
- **status.lastAction**: Outcome of the most recent action; persists until the
  next action overwrites it, regardless of any observable state changes (e.g. a
  direct `docker stop`) in between.
- **status.lastAction.action**: The last observed action.
- **status.lastAction.state**: The result of the last action; either `Succeeded`,
  `Failed`, or an empty string if it is still in progress.
- **status.lastAction.error**: The error message; only set if `state` is
  `Failed`.
- **status.lastAction.observedAt**: The timestamp when the action was initially
  observed.
- **status.lastAction.completedAt**: The timestamp when the action was completed.
- **status.conditions**: Status conditions; to be documented.

### Container state

`status.status` always reflects the actual engine state. The engine
writes status on every sync and never writes the container's spec, so a
restart policy, an out-of-band `docker start` or `nerdctl start`, and
explicit actions all converge through the same observed path.

The Container API is deliberately status-only. Early designs used a
level-triggered `spec.state` field, but Docker's restart policy and
direct CLI writes are concurrent writers, and a level-trigger fought
them both. Actions are now expressed as one-shot annotations (see
below).

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
  name: magical_gates
  namespace: k8s.io
  state: running
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
status:
  name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
  conditions:
  - type: Settled
    status: True
    reason: ContainerCreated
  - type: Failed
    status: False
```

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: No restrictions on the name; clients may use the
  `generateName` functionality if desired.
- **spec.name**: The desired container name; if unset, a random container name
  will be generated.
- **spec.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **spec.state**: Desired container state; defaults to `running`.
- **spec.path**: The path to the executable for PID 1 in the container; defaults
  to image entry point / command.
- **spec.args**: The arguments to the executable for PID 1 in the container;
  defaults to image entry point / command.
- **spec.image**: The image reference; can be a tag (`leap:16`) or an image hash.
- **spec.ports**: The ports to expose; merged with image defaults.
- **spec.labels**: Container labels to set; merged with image labels.
- **status.name**: The resulting `metadata.name` of the container object, which
  is the same as the container ID.  It must be in the same Kubernetes namespace
  as the `ContainerCreateRequest`.
- **status.conditions**: Status conditions.

If `spec.namespace` / `spec.name` duplicates an existing container, a
`CreateFailed` status is set with some details.

An admission controller will ensure that we cannot have multiple
`ContainerRequest` objects at the same time for the same containerd
`name`/`namespace` pair.

#### Change container state

Set the `containers.rancherdesktop.io/action` annotation on the
`Container` to request a one-shot action. The engine reconciler reads
the annotation, dispatches the matching call to the container engine,
records the outcome in `status.lastAction`, and removes the annotation.

Valid values: `start`, `stop`, `pause`, `unpause`, `restart`.

A single annotation holds at most one pending action. Writing a new
value replaces the old, so a user who requests `pause` and then
`unpause` before the reconciler has run will see only the unpause. No
queue, no accumulating history.

The controller calls the engine before patching `status.lastAction`
and removing the annotation, so a crash mid-flight leaves the
annotation in place and the next reconcile replays the action. Start,
stop, pause, and unpause are idempotent against a container already in
the target state, so replay is safe. Restart has no target state to
match: a replay sends the container's stop signal and waits the grace
period a second time, which the controller cannot distinguish from a
deliberate re-request.

If the engine call fails (for example, `pause` on a container that is
not running), the reconciler still removes the annotation and records
the failure in `status.lastAction`:

```yaml
status:
  lastAction:
    action: pause
    state: Failed
    error: "Error response from daemon: Container 8eb6f2 is not running"
    observedAt: "2026-04-15T10:30:00Z"
    completedAt: "2026-04-15T10:30:00Z"
```

The GUI is the intended caller for these actions. CLI users should
reach for `docker start`, `docker stop`, or their `nerdctl`
equivalents, instead: the engine mirrors its own state back into
`status.status` either way.

#### Fetch container logs
An endpoint at `/passthrough/.../logs/${container}` will speak WebSocket;
messages are one way, as stream of bytes; messages should not be buffered.
Message text must be UTF-8 encoded.  The last portion of the path must be the
`Container` mirror's name, which is the full container ID for every engine
that produces one that is a valid object name.

The following query parameters are accepted:

Parameter | Description | Default
--- | --- | ---
`tail` | Only print the given number of lines (before following). | All
`follow` | Follow the log stream. | `true`

#### Exec (shell) in container
An endpoint at `/passthrough/.../exec` will speak WebSocket; messages are
bidirectional, unbuffered binary as in the logs endpoint.  Any text must be
UTF-8 encoded.

#### Delete container
Delete the `Container` object; a finalizer will be used to delete the container,
at which point the `Container` object will actually be deleted.

## Images

`Image` objects reflect images in the container engine.  Each tag is represented
as a new `Image` object; therefore, there may be multiple `Image` objects for
the same image ID (one per tag).  If an image without any tags exists, that will
be represented by an `Image` object without `.status.repoTag`.

containerd names each record by the reference it was registered under, and a
single pull through the CRI plugin registers up to three: the image config
digest, the repo tag, and the repo digest.  A pull that names no tag, such as a
pod pinned to a digest, registers only the first and last.  Each record becomes
its own `Image` mirror sharing one `.status.id`; the tag one sets
`.status.repoTag`, the repo digest one sets `.status.repoDigests`, and the
config digest one sets neither, so a client keying on `repoTag` sees it as
untagged.

`.status.size` is engine-reported and the two engines do not measure the same
thing: moby reports the uncompressed size of the image, while containerd sums
the compressed blob sizes its manifest declares.  The same image therefore
shows a smaller size under containerd.  Compare sizes within one engine, never
across a backend switch.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Image
metadata:
  namespace: rancher-desktop
  name: img-2b0d7f4e7d2f2e2d3c6f0a8a4b5a6c7d8e9f0a1b2c3d4e5f607182a3b4c5d6e7
status:
  namespace: moby
  id: 'sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b'
  repoDigests:
  - registry.opensuse.org/opensuse/leap@sha256:999adf320e40662dc96119a14f07459af9959a081d10ccab7c405257030ab96b
  repoTag: 'registry.opensuse.org/opensuse/leap:latest'
  createdAt: "2025-11-17T03:14:16Z"
  architecture: arm64
  os: linux
  size: 45150437
  labels:
    org.opensuse.base.vendor: openSUSE Project
  conditions: []
```

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: A `img-` prefix followed by a SHA-256 hash.  If the image
  has a tag, it is the hash over the image id (`status.id`), followed by a null
  byte, followed by the tag (`status.repoTag`).  If the image is dangling (i.e.
  no tags), it is the hash of the image id (`status.id`) by itself.
- **status.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **status.id**: The raw image ID, including the `sha256:` prefix (or whichever
  is correct for the image).
- **status.repoDigests**: The digests emitted by the repository.
- **status.repoTag**: The tag of the image; as described above, if the image has
  multiple tags, then multiple `Image` objects would be generated.  If this is a
  dangling image (no tags), this is unset.
- **status.createdAt**: The time the image was created; may be unset.
- **status.architecture**: The architecture of this image; if a tag contains
  multiple architectures, each has a unique image ID, and therefore multiple
  `Image`s.
- **status.os**: The OS of the image; as with `status.architecture`, images with
  multiple OSes have multiple `Image`s.
- **status.size**: The size of the image in bytes; required.
- **status.labels**: Any labels set on the image.
- **status.conditions**: Status conditions; none are defined at this time.

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
  namespace: moby
  repoTag: 'registry.opensuse.org/opensuse/leap:latest'
status:
  lastUpdateTime: "2025-11-17T03:14:16Z"
  start: 0
  current: 10
  total: 100
  units: bytes
  conditions:
  - type: Settled
    status: True
    reason: ImagePulled
  - type: Failed
    status: False
```

- **spec.namespace**: Refers to a [`ContainerNamespace`](#namespaces) object
- **spec.repoTag**: Reference to image to pull.
- **status.lastUpdateTime**: The last time any progress has been made.
- **status.start**: Initial value of the progress
- **status.current**: Current progress value
- **status.total**: Total progress value
- **status.units**: Units for start/current/total

Status conditions:

<table>
<tr><th>Type<th>Reason<th>Status<th>Description
<tr><td rowspan=3>Settled
    <td>ImagePulled<td>True<td>image has been pulled
<tr><td>Pulling<td>False<td>image is being pulled
<tr><td>Errored<td>True<td>image pull has failed
<tr><td rowspan=5>Failed
    <td>Succeeded<td>False<td>image has been pulled
<tr><td>PullFailed<td>True<td>image pull has failed; see `message`
<tr><td>InvalidArgument<td>True<td>image specification was not accepted
<tr><td>Unauthorized<td>True<td>authentication issue pulling image
<tr><td>PullTimeout<td>True<td>the image pull has timed out
</table>

The containerd backend does not pull images, so under
`containerEngine.name=containerd` every `ImagePullRequest` fails with reason
`PullFailed`.

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
  imageRef: img-2b0d7f4e7d2f2e2d3c6f0a8a4b5a6c7d8e9f0a1b2c3d4e5f607182a3b4c5d6e7
status:
  conditions:
  - type: Settled
    status: True
    reason: ImagePushed
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
  imageRef: img-2b0d7f4e7d2f2e2d3c6f0a8a4b5a6c7d8e9f0a1b2c3d4e5f607182a3b4c5d6e7
status:
  conditions:
  - type: Settled
    status: True
    reason: Finished
  - type: Failed
    status: False
  result:
    # Just dump the raw Trivy result JSON here (without converting to YAML).
    '{ ... }'
```

#### Untag image
Delete the `Image` object through the K8s API; the finalizer removes the
matching reference from the engine.

The two engines differ in what that leaves behind. Docker keeps the underlying
image while another tag or a running container references it, so removing one
tag may leave the image in place. containerd has no such protection: deleting
the record a mirror was built from succeeds even while a container is running
on it, and the container keeps its snapshot until it is deleted itself.

On Docker the engine controller also mirrors untag events in the reverse
direction: on an `untag` event it re-inspects the image and removes any K8s
`Image` resources whose `.status.repoTag` is no longer in Docker's tag list. If
the image becomes dangling, a new `Image` object without `.status.repoTag`
takes its place. containerd needs none of this, because its `ImageDelete` event
names the record directly and each record already has its own mirror.

#### Delete untagged image
Delete the `Image` object (which does not have any `.status.repoTag` set).  An
admission controller must be set up so that this is not allowed if there is a
running container that uses that image.

## Volumes

Volumes are a moby concept, so these resources exist only on that backend.
Docker has one namespace, so every `Volume` shares it and the controller writes
`moby`. containerd has no volume API, and switching to it prunes every `Volume`
mirror on the next full sync.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Volume
metadata:
  name: vol-d404559327842434dee6f7a10d8998594be5b49a7ef9a91a42ca2b3d0174ab9d
  namespace: rancher-desktop
status:
  namespace: moby
  name: volume-name
  createdAt: "2025-11-17T03:14:16Z"
  driver: local
  mountpoint: /var/lib/docker/volumes/volume-name/_data
  labels: {}
  scope: local
  options: {}
```

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: A `vol-` prefix, followed by the SHA-256 hash of the
  original Docker/containerd volume name.
- **status.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **status.name**: The docker / nerdctl volume name.  This may contain uppercase
  and underscores, which would not be valid in Kubernetes object names.
- **status.createdAt**: The time the volume was created; unset if this is not
  available.
- **status.driver**, **status.mountpoint**, **status.scope**, **status.options**:
  Various information reported by the container engine.
- **status.labels**: Labels for the volume.

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
  namespace: moby # engine namespace; the only one Docker has
  driver: local
status:
  conditions:
  - type: Settled
    status: True
    reason: VolumeCreated
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

## Compose Projects

`ComposeProject` objects do not reflect actual container engine objects; instead, they
reflect `docker compose` projects.

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ComposeProject
metadata:
  name: moby.my-project
  namespace: rancher-desktop
status:
  namespace: moby
  name: my-project
  workingDir: /opt/foo/project-dir
  configs: []
  containers:
  - name: 8eb6f2cf72b6616aa743cf9187f350af84c9749dab65474db2530f26745d2ef3
    uid: 239ef6a0-63bd-4e87-9a7e-c5d4435ddcd4
  conditions: []
```

- **metadata.name**: This name must be constructed by the following:
  - The candidate name is the `status.namespace`, followed by a dot, followed by
    the compose project name (i.e. `status.name`).
  - If the candidate name is a valid Kubernetes name (that is, runs of
    lower-case alphanumeric characters or dash, but does not start or end with
    dash; each run is joined by a dot), then use it as `metadata.name`.
  - Otherwise, this is `cmp-` followed by the lower-case SHA-256 hash of the
    candidate name.
- **status.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **status.name**: The compose project name.
- **status.workingDir**: Optional; the compose project directory on the host on
  which the RDD process runs, as an absolute path.
- **status.configs**: Optional; the list of compose files used to create the
  project.  Relative to `status.workingDir`, which means it's also a path on the
  host.
- **status.containers**: A list of containers that are part of this project.  The
  `name` is the `metadata.name` of the object (i.e. the container ID).  Each
  `name` must be unique.  The `uid` is the object UID (i.e. `.metadata.uid`),
  used to track when the object has been recreated.
- **status.conditions**: The normal status conditions; see [below](#status-conditions)

`ComposeProject`s should not be manually created; they should only be created by
the reconciler, when it detects a container with the normal compose labels.  To
create `ComposeProject`s, the user may create a
[`ComposeUpRequest`](#composeuprequest) object.

When containers are detected to be part of a project, the `status.containers`
field would be updated to indicate which containers were found.  The
`HasMembers` status condition would also be set to `True` to indicate that
containers have been detected.

Both `status.workingDir` and `status.configs` are set from observed containers;
if multiple containers are part of the same project, but they disagree on such
fields, it is undefined which will be used.  As such, these are informational
only.

Associated containers being deleted would similarly update `status.containers`;
once the last item has been removed, `HasMembers` would be set to `False`.  The
`ComposeProject` will eventually be deleted after `HasMembers` transitions to
`False`.

### Status Conditions

The following status conditions are defined:

<table>
<tr><th>Type<th>Reason<th>Status<th>Description
<tr><td rowspan=3>HasMembers
    <td>Found<td>True<td>Objects matching this project were found.
<tr><td>Deleted<td>False<td>The last object for this project was deleted; this project will be reaped.
<tr><td>Calculating<td>Unknown<td>Action is being processed.
</table>

### Delete `ComposeProject`

The `engine.rancherdesktop.io/mirror` finalizer as described above is used to
monitor `ComposeProject` objects being deleted.  Deleting the `ComposeProject`
will cause `docker compose down --remove-orphans` to be run.  Any associated
volumes and images will also be deleted (i.e. using the equivalent of
`docker compose down --rmi all --volumes`).  Because `status.workingDir` and
`status.configs` may not be available, this will only be able to delete
resources with the correct labels.  The `ComposeProject` itself will be deleted
once that succeeds (because `HasMembers` will become `False` at that point).
If the `docker compose down` fails, it may be retried later.

Note: `docker swarm` may create compose projects as part of their mechanism;
deleting projects created this way is likely to leave behind non-compose
resources.

### Compose Actions

#### `ComposeUpRequest`

```yaml
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ComposeUpRequest
metadata:
  name: moby.my-project
  namespace: rancher-desktop
spec:
  namespace: moby
  name: my-project
  workingDir: /opt/foo/project-dir
  configs: []
status:
  conditions: []
```

Creating a `ComposeUpRequest` will trigger `docker compose up` and creation of
a `ComposeProject`.

- **metadata.name**: The name must be constructed in the same way as a
  [`ComposeProject`](#compose-projects) resource, based on `spec.namespace` and
  `spec.name`.  That is, the resulting `ComposeProject` object will have the
  same name as the `ComposeUpRequest`.
- **spec.namespace**: The containerd namespace; same as the `status.name` of a
  [`ContainerNamespace`](#namespaces) object.
- **spec.name**: The compose project name.
- **spec.workingDir**: The compose project directory on the host (i.e. relative
  to where the RDD process runs).  Used to look up any files needed.
- **spec.configs**: Optional; the list of compose files used to create the
  project.  Relative to `spec.workingDir`, which means it's also a path on the
  host.  Defaults to the `docker compose` defaults.
- **status.conditions**: The normal status conditions; see [below](#status-conditions-1)

##### Status Conditions

The following status conditions are defined:

<table>
<tr><th>Type<th>Reason<th>Status<th>Description
<tr><td rowspan="3">Settled
    <td>Succeeded<td>True<td><tt>docker compose up</tt> succeeded.
<tr><td>Failed<td>True<td><tt>docker compose up</tt> failed.
<tr><td>Running<td>False<td><tt>docker compose up</tt> is still running.
<tr><td>Failed<td>Failed<td>True<td><tt>docker compose up</tt> failed.
</table>

The `ComposeUpRequest` object will be automatically reaped some time after the
`Settled` status condition has been set to `True`, whether it has succeeded or
not.
