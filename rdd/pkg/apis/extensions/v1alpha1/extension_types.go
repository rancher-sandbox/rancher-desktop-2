// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExtensionKind is the Kind string for Extension resources.
const ExtensionKind = "Extension"

// Extension condition types.
//
// Installed, Started, and Ready each track a different phase of the
// extension lifecycle; Ready aggregates the overall state (including
// uninstall) and is the condition that consumers should generally watch.
const (
	// ExtensionConditionInstalled reports whether the extension image has
	// been downloaded, extracted, and had its post-install script (if any)
	// run successfully.
	ExtensionConditionInstalled = "Installed"

	// ExtensionConditionStarted reports whether the extension's containers
	// (if any) have been started; it also tracks the extension being stopped.
	ExtensionConditionStarted = "Started"

	// ExtensionConditionReady aggregates Installed and Started (and
	// uninstall progress) into the overall extension lifecycle state.
	ExtensionConditionReady = "Ready"

	// ExtensionConditionContainerEngineReady reports whether the container
	// engine (e.g., Docker or containerd) is ready for extension operations.
	ExtensionConditionContainerEngineReady = "ContainerEngineReady"

	// ExtensionConditionExtracted reports whether the extension image has
	// been extracted onto disk.  This is a sub-phase of Installed, tracked
	// separately so it can be reconciled independently of downloading and
	// running the post-install script.
	ExtensionConditionExtracted = "Extracted"
)

// Reasons for the Installed condition.
const (
	// ExtensionInstalledReasonResolving means spec.image is being resolved
	// (defaulting a missing tag) into status.image.
	ExtensionInstalledReasonResolving = "Resolving"
	// ExtensionInstalledReasonResolveFailed means spec.image could not be
	// resolved, e.g. because it is not a valid image reference.  Terminal.
	ExtensionInstalledReasonResolveFailed = "ResolveFailed"
	// ExtensionInstalledReasonDownloading means the extension image is
	// being downloaded.
	ExtensionInstalledReasonDownloading = "Downloading"
	// ExtensionInstalledReasonDownloadFailed means the extension image
	// failed to download.  Terminal.
	ExtensionInstalledReasonDownloadFailed = "DownloadFailed"
	// ExtensionInstalledReasonExtracting means the extension files are
	// being extracted.
	ExtensionInstalledReasonExtracting = "Extracting"
	// ExtensionInstalledReasonExtractFailed means the extension extraction
	// failed, or the image was invalid.  Terminal.
	ExtensionInstalledReasonExtractFailed = "ExtractFailed"
	// ExtensionInstalledReasonExtracted means the extension extraction
	// completed successfully.
	ExtensionInstalledReasonExtracted = "Extracted"
	// ExtensionInstalledReasonPostInstallRunning means the extension's
	// post-install script is running.
	ExtensionInstalledReasonPostInstallRunning = "PostInstallRunning"
	// ExtensionInstalledReasonPostInstallFailed means the extension's
	// post-install script failed.  Terminal.
	ExtensionInstalledReasonPostInstallFailed = "PostInstallFailed"
	// ExtensionInstalledReasonInstalled means the image has been installed
	// successfully.  Terminal.
	ExtensionInstalledReasonInstalled = "Installed"
	// ExtensionInstalledReasonPreUninstallRunning means the extension's
	// pre-uninstall script is running.
	ExtensionInstalledReasonPreUninstallRunning = "PreUninstallRunning"
	// ExtensionInstalledReasonDeleting means the extension files are being
	// deleted.
	ExtensionInstalledReasonDeleting = "Deleting"
	// ExtensionInstalledReasonDeleteFailed means the extension files could
	// not be deleted.  Terminal.
	ExtensionInstalledReasonDeleteFailed = "DeleteFailed"
	// ExtensionInstalledReasonUninstalled means the extension was removed;
	// the object will go away.  Terminal.
	ExtensionInstalledReasonUninstalled = "Uninstalled"
)

// Reasons for the Started condition.
const (
	// ExtensionStartedReasonInstalling means the extension is still being
	// installed.
	ExtensionStartedReasonInstalling = "Installing"
	// ExtensionStartedReasonStarting means the extension is being started.
	ExtensionStartedReasonStarting = "Starting"
	// ExtensionStartedReasonStartFailed means the extension failed to
	// start.  Terminal.
	ExtensionStartedReasonStartFailed = "StartFailed"
	// ExtensionStartedReasonStarted means the extension has been started.
	// Terminal.
	ExtensionStartedReasonStarted = "Started"
	// ExtensionStartedReasonStopping means the extension is being stopped.
	ExtensionStartedReasonStopping = "Stopping"
	// ExtensionStartedReasonStopped means the extension has been stopped.
	// Terminal.
	ExtensionStartedReasonStopped = "Stopped"
)

// Reasons for the Ready condition.
const (
	// ExtensionReadyReasonCreated means install has not started yet.
	ExtensionReadyReasonCreated = "Created"
	// ExtensionReadyReasonInstalling means the extension is being
	// installed.
	ExtensionReadyReasonInstalling = "Installing"
	// ExtensionReadyReasonStarting means the extension is being started.
	ExtensionReadyReasonStarting = "Starting"
	// ExtensionReadyReasonReady means the extension is running and ready.
	// Terminal.
	ExtensionReadyReasonReady = "Ready"
	// ExtensionReadyReasonStopping means the extension is being stopped.
	ExtensionReadyReasonStopping = "Stopping"
	// ExtensionReadyReasonUninstalling means the extension is being
	// removed.
	ExtensionReadyReasonUninstalling = "Uninstalling"
	// ExtensionReadyReasonBroken means user interaction is required.
	// Terminal.
	ExtensionReadyReasonBroken = "Broken"
)

// Reasons for the ContainerEngineReady condition.
const (
	// ExtensionContainerEngineReadyReasonNotReady means the container engine is
	// not currently ready.
	ExtensionContainerEngineReadyReasonNotReady = "NotReady"
	// ExtensionContainerEngineReadyReasonReady means the container engine is
	// ready for use.
	ExtensionContainerEngineReadyReasonReady = "Ready"
)

// Reasons for the Extracted condition.
const (
	// ExtensionExtractedReasonEngineNotReady means the container engine is not
	// ready for extension operations.  Terminal.
	ExtensionExtractedReasonEngineNotReady = "EngineNotReady"
	// ExtensionExtractedReasonPreparing means extraction is setting up.
	ExtensionExtractedReasonPreparing = "Preparing"
	// ExtensionExtractedReasonMetadata means extension metadata is
	// being extracted.
	ExtensionExtractedReasonMetadata = "ExtractingMetadata"
	// ExtensionExtractedReasonIcon means the extension icon is
	// being extracted.
	ExtensionExtractedReasonIcon = "ExtractingIcon"
	// ExtensionExtractedReasonUI means the extension user
	// interface files are being extracted.
	ExtensionExtractedReasonUI = "ExtractingUI"
	// ExtensionExtractedReasonExecutable means the extension host
	// executables are being extracted.
	ExtensionExtractedReasonExecutable = "ExtractingExecutable"
	// ExtensionExtractedReasonCompleted means the extension has been
	// extracted.  Terminal.
	ExtensionExtractedReasonCompleted = "Completed"
	// ExtensionExtractedReasonFailed means the extraction has failed. Terminal.
	ExtensionExtractedReasonFailed = "Failed"
)

// ExtensionSpec defines the desired state of Extension.
type ExtensionSpec struct {
	// image is the image reference for the extension. The tag is optional;
	// if omitted, status.image reports the resolved reference (the image
	// with the highest semver tag, or "latest" if none are available).
	//
	// metadata.name must be the sanitized version of this value (excluding
	// the tag); resource creation or update is rejected otherwise.
	//
	// +required
	Image string `json:"image"`
}

// ExtensionDashboardTabStatus describes the dashboard tab UI exposed by an
// extension.
type ExtensionDashboardTabStatus struct {
	// title is the title to display for this extension in the side bar.
	//
	// +required
	Title string `json:"title"`
	// src is the initial page to load for this extension; relative to
	// `.../ui/` on the passthrough endpoint for this extension.
	//
	// +required
	Src string `json:"src"`
	// socket indicates that the UI should set up a loopback TCP listener
	// forwarding to the `.../socket` passthrough endpoint for this
	// extension.
	//
	// +optional
	Socket bool `json:"socket,omitempty"`
}

// ExtensionUIStatus describes the user interface exposed by an extension.
// Only valid after the extension has been started.
type ExtensionUIStatus struct {
	// dashboardTab describes the dashboard tab UI exposed by this
	// extension, if any.
	//
	// +optional
	DashboardTab *ExtensionDashboardTabStatus `json:"dashboardTab,omitempty"`
}

// ExtensionStatus defines the observed state of Extension.
type ExtensionStatus struct {
	// image is the resolved image reference that is actually used,
	// including the tag.  This is mostly useful when spec.image does not
	// contain a tag.
	//
	// +optional
	Image string `json:"image,omitempty"`

	// metadata is the contents of metadata.json in the extension image.
	// Only available once the extension has been downloaded.
	//
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	Metadata *apiextensionsv1.JSON `json:"metadata,omitempty"`

	// ui describes the user interface exposed by this extension. Only
	// valid after the extension has been started.
	//
	// +optional
	UI *ExtensionUIStatus `json:"ui,omitempty"`

	// conditions represent the state of the extension.
	//
	// Known condition types include:
	// - "Installed": the extension image has been downloaded, extracted,
	//   and had its post-install script (if any) run successfully.
	// - "Started": the extension's containers (if any) have been started.
	// - "Ready": aggregates the overall extension lifecycle state.
	// - "ContainerEngineReady": the container engine can be used.
	//
	// The status of each condition is one of True, False, or Unknown.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:ac:generate=true
// +kubebuilder:resource:categories="all"
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=.status.image
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.status.image`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// Extension is the Schema for the extensions API.
type Extension struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	Spec   ExtensionSpec   `json:"spec"`
	Status ExtensionStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ExtensionList contains a list of Extension.
type ExtensionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Extension `json:"items"`
}

func init() {
	registerTypes(&Extension{}, &ExtensionList{})
}
