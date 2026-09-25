# Rancher Desktop Extensions API

The `extensions.rancherdesktop.io` API group contains resources for extending
Rancher Desktop Daemon as well as the UI.

## Extension Resources

`Extension` objects are the representation of the extension.

```yaml
apiVersion: extensions.rancherdesktop.io/v1alpha1
kind: Extension
metadata:
  namespace: rancher-desktop # The namespace given in `app.spec.namespace`
  name: ghcr.io.rancher-sandbox-rancher-desktop-rdx-host-api-test
spec:
  image: ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test
status:
  image: ghcr.io/rancher-sandbox/rancher-desktop/rdx-host-api-test:latest
  metadata: {}
  ui:
    dashboardTab:
      title: string
      src: string
      socket: true
  conditions:
  - type: Installed
    status: "True"
    reason: Installed
    message: Extension image downloaded
  - type: Started
    status: "True"
    reason: Started
    message: Extension has been started
  - type: Ready
    status: "True"
    reason: Ready
    message: Extension is ready for use
```

- **metadata.name**: The name of the extension; this is the sanitized version of
  the image, excluding the tag.

- **spec.image**: Required; the image of the extension.  The tag is optional
  here; `status.image` is the resolved reference with tag.  Resource creation or
  update will be rejected if the `metadata.name` does not match the sanitized
  version of this value (excluding tag).  This must be a named reference, and
  not a digest reference.

  The sanitization algorithm involves:
  - assert that the image is a valid reference
  - remove `docker.io/library/` prefix if present
  - convert the registry (if present) to lower case
  - strip the tag (and the `:` before it)
  - replace `:` with `.` (likely for the registry port number)
  - replace non-alphanumeric characters (excluding `.`) with `-`

  Note that this means it is possible to update the tag, but it is not possible
  to change the image name (the part excluding the tag).  Multiple image
  references can be sanitized to the same value (e.g. a `/` vs `-` difference);
  this simply means they cannot both be installed as extensions at the same time.

- **status.image**: The resolved image that is actually used, including the tag.
  This is mostly useful when `spec.image` does not contain a tag; in that case,
  the registry is queried for the image with the highest semver tag, or `latest`
  if none are available.

- **status.metadata**: The contents of `metadata.json` in the extension image.
  Only available once the extension has been downloaded.

- **status.ui**: Optional; information for showing user interface for this
  extension.  Only valid after the extension has been started.
- **status.ui.dashboardTab.title**: The title to display for this extension.
- **status.ui.dashboardTab.src**: The initial page to load for this extension;
  this is relative to `.../ui/` described in the
  [_User Interface_](#user-interface) section below.
- **status.ui.dashboardTab.socket**: Boolean, optional; if set to `true`, then
  the UI should set up a loopback TCP listener forwarding to the `.../socket`
  endpoint described in the [_User Interface_](#user-interface) section below.

### Status Conditions

The _Description_ column describes the state; it is not meant to match the
status condition's `message` field, though that may be the case sometimes.
The _Terminal_ column indicates that the given status condition type will not
further transition without some change to the extension resource spec.

<table>
<tr><th>Type<th>Reason<th>Status<th>Description<th>Terminal</tr>
<tr><td rowspan=14>Installed
      <td>Resolving<td>False<td>Image reference is being resolved<td>
  <tr><td>ResolveFailed<td>False<td>Image reference could not be resolved<td>:heavy_check_mark:
  <tr><td>Downloading<td>False<td>Image is being downloaded<td>
  <tr><td>DownloadFailed<td>False<td>Extension image failed to download<td>:heavy_check_mark:
  <tr><td>Extracting<td>False<td>Extension files are being extracted<td>
  <tr><td>ExtractFailed<td>False<td>Extension extraction failed<td>:heavy_check_mark:
  <tr><td>Extracted<td>False<td>Extension files have been extracted
  <tr><td>PostInstallRunning<td>False<td>Running extension post-install script<td>
  <tr><td>PostInstallFailed<td>False<td>Extension post-install script failed<td>:heavy_check_mark:
  <tr><td>Installed<td>True<td>Image has been installed successfully<td>:heavy_check_mark:
  <tr><td>PreUninstallRunning<td>False<td>Running extension pre-uninstall script<td>
  <tr><td>Deleting<td>False<td>Extension files being deleted<td>
  <tr><td>DeleteFailed<td>False<td>Extension files could not be deleted; deletion is automatically retried<td>
  <tr><td>Uninstalled<td>True<td>Extension was removed; object will go away<td>:heavy_check_mark:
<tr><td rowspan=8>Extracted
  <td>EngineNotReady<td>False<td>The container engine is not available
  <tr><td>Preparing<td>False<td>Extraction is setting up
  <tr><td>ExtractingMetadata<td>False<td>Extension metadata is being extracted
  <tr><td>ExtractingIcon<td>False<td>Extension icon is being extracted
  <tr><td>ExtractingUI<td>False<td>Extension user interface files are being extracted
  <tr><td>ExtractingExecutable<td>False<td>Extension host executables are being extracted
  <tr><td>Completed<td>True<td>Extension has been extracted<td>:heavy_check_mark:
  <tr><td>Failed<td>False<td>Extension extraction has failed<td>:heavy_check_mark:
<tr><td rowspan=6>Started
      <td>Installing<td>False<td>Extension is still being installed<td>
  <tr><td>Starting<td>False<td>Extension is being started<td>
  <tr><td>StartFailed<td>False<td>Extension failed to start<td>:heavy_check_mark:
  <tr><td>Started<td>True<td>Extension has been started<td>:heavy_check_mark:
  <tr><td>Stopping<td>False<td>Extension is being stopped<td>
  <tr><td>Stopped<td>True<td>Extension is stopped<td>:heavy_check_mark:
<tr><td rowspan=7>Ready
      <td>Created<td>False<td>Install has not started yet<td>
  <tr><td>Installing<td>False<td>Extension is being installed<td>
  <tr><td>Starting<td>False<td>Extension is being started<td>
  <tr><td>Ready<td>True<td>Extension is running and ready<td>:heavy_check_mark:
  <tr><td>Stopping<td>False<td>Extension is being stopped<td>
  <tr><td>Uninstalling<td>False<td>Extension is being removed<td>
  <tr><td>Broken<td>False<td>User interaction required<td>:heavy_check_mark:
<tr><td rowspan=2>ContainerEngineReady
      <td>NotReady<td>False<td>Container engine has not been synchronized
  <tr><td>Ready<td>True<td>Container engine is ready for use
</table>

Notes:
- On uninstall, pre-uninstall script failures are ignored; the uninstall
  proceeds regardless.

### User Interface

For interacting with the user interface, the passthrough endpoint is used, where
`${extension}` refers to the `.metadata.name` of the extension:

- `/passthrough/.../extensions/${extension}/ui/`: HTTP server for the dashboard
  UI.
- `/passthrough/.../extensions/${extension}/icon.png`: Icon for the extension in
  the main window side bar; may not actually be PNG.
- `/passthrough/.../extensions/${extension}/socket`: WebSocket end point for the
  forwarded socket; the client is expected to expose a TCP port locally, then
  for each inbound connection create a new WebSocket connection to this end
  point and act as a dumb pipe.  This is optional.

### Extension Actions

#### Delete Extension

Deleting the extension causes it to be uninstalled.
