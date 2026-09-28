> [!CAUTION]
> This API has not been implemented yet

# Rancher Desktop Extensions API

The `extensions.rancherdesktop.io` API group contains resources for extending
Rancher Desktop Daemon as well as the UI.

## Extension Resources

Each `Extension` object represents a single Rancher Desktop extension.

```yaml
apiVersion: extensions.rancherdesktop.io/v1alpha1
kind: Extension
metadata:
  namespace: rancher-desktop
  name: can-be-called-anything
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
  labels:
    org.opencontainers.image.title: "openSUSE Leap 15.6 Base Container",
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

- **metadata.namespace**: the Kubernetes namespace; this must be the same value
  as the [App](api_app.md#app-object) resources's `spec.namespace` field.
- **metadata.name**: The name of the extension; this is free-form.

- **spec.image**: Required; the image of the extension.  The image reference
  must be named (cannot just be a lone digest), and the tag and digest are
  optional.  `status.image` is the resolved reference with tag.  There must not
  be multiple extensions referring to the same image (ignoring the tag and
  digest) after normalization / familiarization (i.e. dropping `docker.io` and
  `library` prefixes).

  It is not legal to update the image to a different name; however, updating the
  tag or digest with the same name is acceptable.

- **status.image**: The resolved image reference that is actually used,
  including the tag. This is mostly useful when `spec.image` does not contain a
  tag; in that case, the registry is queried for the image with the highest
  semver tag, or `latest` if none are available.

- **status.metadata**: The contents of `metadata.json` in the extension image,
  as a string.  Only available once the extension has been downloaded.

- **status.ui**: Optional; information for showing user interface for this
  extension.  Only valid after the extension has been started.
- **status.ui.dashboardTab.title**: The title to display for this extension.
- **status.ui.dashboardTab.src**: The initial page to load for this extension;
  this is relative to `.../ui/` described in the
  [_User Interface_](#user-interface) section below.
- **status.ui.dashboardTab.socket**: Boolean, optional; if set to `true`, then
  the `.../socket` endpoint described in the [_User Interface_](#user-interface)
  section below is valid.

- **status.labels**: The labels from the extension image.  This may include
  values to be displayed to the user.

### Status Conditions

The _Description_ column describes the state; it is not meant to match the
status condition's `message` field, though that may be the case sometimes.
The _Terminal_ column indicates that the given status condition type will not
further transition without some change to the extension resource spec (or the
extension is deleted, and possibly recreated).  The _Status_ column is implied
to be _False_ if not given.

<table>
<tr><th>Type<th>Reason<th>Status<th>Description<th>Terminal</tr>
<tr><td rowspan=10>Installed
      <td>Resolving<td><td>Image reference is being resolved<td>
  <tr><td>Downloading<td><td>Image is being downloaded<td>
  <tr><td>Extracting<td><td>Extension files are being extracted; see <i>Extracted</i> type for details<td>
  <tr><td>Extracted<td><td>Extension files have been extracted<td>
  <tr><td>PostInstallRunning<td><td>Running extension post-install script<td>
  <tr><td>Installed<td>True<td>Image has been installed successfully<td>:heavy_check_mark:
  <tr><td>PreUninstallRunning<td><td>Running extension pre-uninstall script<td>
  <tr><td>Deleting<td><td>Extension files being deleted<td>
  <tr><td>Uninstalled<td><td>Extension was removed; object will go away<td>:heavy_check_mark:
  <tr><td>Failed<td><td>Extension installation failed<td>:heavy_check_mark:
<tr><td rowspan=8>Extracted
  <td>EngineNotReady<td><td>The container engine is not available; extraction cannot continue<td>
  <tr><td>Preparing<td><td>Extraction is setting up<td>
  <tr><td>ExtractingMetadata<td><td>Extension metadata is being extracted<td>
  <tr><td>ExtractingIcon<td><td>Extension icon is being extracted<td>
  <tr><td>ExtractingUI<td><td>Extension user interface files are being extracted<td>
  <tr><td>ExtractingExecutable<td><td>Extension host executables are being extracted<td>
  <tr><td>Completed<td>True<td>Extension has been extracted<td>:heavy_check_mark:
  <tr><td>Failed<td><td>Extension extraction has failed<td>:heavy_check_mark:
<tr><td rowspan=6>Started
      <td>Installing<td><td>Extension is still being installed<td>
  <tr><td>Starting<td><td>Extension is being started<td>
  <tr><td>StartFailed<td><td>Extension failed to start<td>:heavy_check_mark:
  <tr><td>Started<td>True<td>Extension has been started<td>:heavy_check_mark:
  <tr><td>Stopping<td><td>Extension is being stopped<td>
  <tr><td>Stopped<td><td>Extension is stopped<td>:heavy_check_mark:
<tr><td rowspan=7>Ready
      <td>Created<td><td>Install has not started yet<td>
  <tr><td>Installing<td><td>Extension is being installed<td>
  <tr><td>Starting<td><td>Extension is being started<td>
  <tr><td>Ready<td>True<td>Extension is running and ready<td>:heavy_check_mark:
  <tr><td>Stopping<td><td>Extension is being stopped<td>
  <tr><td>Uninstalling<td><td>Extension is being removed<td>
  <tr><td>Broken<td><td>User interaction required<td>:heavy_check_mark:
<tr><td rowspan=2>ContainerEngineReady
      <td>NotReady<td><td>Container engine is not available<td>
  <tr><td>Ready<td>True<td>Container engine is ready for use<td>
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
- `/passthrough/.../extensions/${extension}/socket`: HTTP server for the
  forwarded socket; for use with the `ddClient.extension.vm.service` API.  Only
  valid when `status.ui.socket` is `True`.

### Extension Actions

#### Delete Extension

Deleting the extension causes it to be uninstalled.
