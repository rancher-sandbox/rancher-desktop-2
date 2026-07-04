// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AppKind is the Kind string for App resources.
const AppKind = "App"

// EngineControllerName is the registry name of the engine controller.
// Both the engine controller's own registration and the App reconciler's
// discovery query reference this constant so they cannot drift.
const EngineControllerName = "engine"

// KubernetesControllerName is the registry name of the Kubernetes context controller.
const KubernetesControllerName = "kubernetes"

// PathManagementControllerName is the registry name of the PATH management controller.
const PathManagementControllerName = "path-management"

// App condition types.
//
// Load-bearing invariant: every condition written to App status must stamp
// ObservedGeneration with the App's generation. `rdd set` filters conditions
// by generation, both to wait on a fresh Settled and to report reconcile
// progress, so an unstamped condition reads as stale and never appears.
const (
	// AppConditionRunning mirrors the LimaVM Running condition: True
	// means the Lima guest has finished booting and SSH is reachable.
	// It says nothing about the container engine socket; consumers
	// that depend on the engine must also check
	// AppConditionContainerEngineReady.
	AppConditionRunning = "Running"

	// AppConditionContainerEngineReady goes True once the engine
	// controller has connected to the container engine socket and
	// completed its initial full sync of Container, Image, and Volume
	// mirrors. The engine controller stamps the App's generation into
	// ObservedGeneration, so `rdd set` can distinguish a stale True
	// from a fresh one.
	AppConditionContainerEngineReady = "ContainerEngineReady"

	// AppConditionKubernetesReady goes True once the k3s API server answers,
	// the node is Ready, and the rancher-desktop-{instance} context is
	// merged into ~/.kube/config. Workload-level readiness (coredns,
	// traefik) is not gated; consumers that need it wait for those
	// Deployments themselves. The condition is only meaningful when
	// spec.kubernetes.enabled is true; when Kubernetes is disabled the
	// condition is absent or False with reason NotApplicable.
	AppConditionKubernetesReady = "KubernetesReady"

	// AppConditionSettled reports whether the reconcile chain has
	// fully caught up with the current spec: observed generations on
	// the feeding conditions match the App's generation, and the VM,
	// engine, and (when spec.kubernetes.enabled is true) Kubernetes
	// have reached a stable state for the desired config. A spec
	// change forces Settled to False; once the chain quiesces, the
	// App reconciler flips it back to True. `rdd set` waits on this
	// condition.
	AppConditionSettled = "Settled"

	// AppConditionPathManagementReady goes True once the PATH management
	// controller has applied spec.application.addPath for the current
	// generation: the bin directory is present in (front/back) or absent from
	// (manual) the user's shell startup files and, on Windows, the user
	// Environment. It stamps the App's generation into ObservedGeneration; a
	// failed edit sets it False with reason Failed, so Settled and `rdd set`
	// can observe the failure instead of reporting success.
	AppConditionPathManagementReady = "PathManagementReady"
)

// Reasons for the Settled condition. Consumers branch on these
// values; the App reconciler also forwards the Running condition's
// reason when LimaVM has not yet reached the desired state (see
// api_app.md).
const (
	// AppSettledReasonSettled means the App has reached the desired state.
	AppSettledReasonSettled = "Settled"

	// AppSettledReasonWaitingForLimaVM means LimaVM has not yet reported a Running condition.
	AppSettledReasonWaitingForLimaVM = "WaitingForLimaVM"

	// AppSettledReasonWaitingForEngine means the engine controller has not yet written ContainerEngineReady.
	AppSettledReasonWaitingForEngine = "WaitingForEngine"

	// AppSettledReasonEngineStale means the engine controller has not yet observed the current generation.
	AppSettledReasonEngineStale = "EngineStale"

	// AppSettledReasonWaitingForKubernetes means the Kubernetes controller has not yet written KubernetesReady.
	AppSettledReasonWaitingForKubernetes = "WaitingForKubernetes"

	// AppSettledReasonKubernetesStale means the Kubernetes controller has not yet observed the current generation.
	AppSettledReasonKubernetesStale = "KubernetesStale"

	// AppSettledReasonApplyingTemplate means the LimaVM has not yet restarted
	// into the current template, so a spec change that rewrote the template is
	// not yet in effect.
	AppSettledReasonApplyingTemplate = "ApplyingTemplate"

	// AppSettledReasonWaitingForPathManagement means the PATH management
	// controller has not yet written PathManagementReady.
	AppSettledReasonWaitingForPathManagement = "WaitingForPathManagement"

	// AppSettledReasonPathManagementStale means the PATH management controller
	// has not yet observed the current generation.
	AppSettledReasonPathManagementStale = "PathManagementStale"
)

// Reasons for the PathManagementReady condition.
const (
	// PathManagementReasonApplied means spec.application.addPath was applied
	// for the current generation.
	PathManagementReasonApplied = "Applied"

	// PathManagementReasonFailed means applying spec.application.addPath
	// failed with a transient error (I/O, permissions, a full disk); the
	// message carries the underlying error. It's repairable, so the reconciler
	// keeps retrying and it gates Settled until it clears.
	PathManagementReasonFailed = "Failed"

	// PathManagementReasonMalformed means a startup file was hand-edited into a
	// state we can't parse (a lone marker, or markers in the wrong order), so the
	// block can't be removed. Only the user can fix the file, so unlike Failed
	// it's permanent: it doesn't gate Settled under manual (which would wedge it
	// forever) and doesn't block App deletion.
	PathManagementReasonMalformed = "Malformed"
)

// Reasons for the KubernetesReady condition.
const (
	// AppKubernetesReasonReady means the API server answers, the node is
	// Ready, and the kubeconfig context is merged into ~/.kube/config.
	AppKubernetesReasonReady = "Ready"

	// AppKubernetesReasonNotApplicable means spec.kubernetes.enabled is false;
	// the condition is set to False with this reason so consumers can
	// distinguish "disabled" from "still starting".
	AppKubernetesReasonNotApplicable = "NotApplicable"

	// AppKubernetesReasonNotRunning means the VM is not running, so k3s
	// cannot be healthy.
	AppKubernetesReasonNotRunning = "NotRunning"

	// AppKubernetesReasonProbing means the controller is still waiting for
	// the k3s API server to respond.
	AppKubernetesReasonProbing = "Probing"

	// AppKubernetesReasonWaitingForNode means the API server answers but no
	// node has reached Ready, so the cluster cannot schedule a workload.
	AppKubernetesReasonWaitingForNode = "WaitingForNode"

	// AppKubernetesReasonMergeFailed means the k3s API server is reachable
	// but merging the instance kubeconfig into ~/.kube/config failed.
	AppKubernetesReasonMergeFailed = "MergeFailed"
)

const (
	// EngineReasonStopped is set on ContainerEngineReady when the engine has
	// stopped and all mirror resources have been cleaned up.
	EngineReasonStopped = "Stopped"

	// EngineReasonNotApplicable is set on ContainerEngineReady when the
	// selected engine mirrors nothing on this platform, which today means
	// containerd on Windows. The condition is forced True so callers waiting
	// on it can finish; gate on the reason before expecting mirrors.
	EngineReasonNotApplicable = "NotApplicable"

	// EngineReasonConnected is set on ContainerEngineReady when the engine is
	// running and mirror resources are in sync.
	EngineReasonConnected = "Connected"

	// EngineReasonConnectFailed is set on ContainerEngineReady when the engine
	// reconciler could not connect to the Docker daemon.
	EngineReasonConnectFailed = "ConnectFailed"
)

// VirtualMachineSpec defines the resource allocation for the Lima VM.
type VirtualMachineSpec struct {
	// cpus is the number of vCPUs to allocate to the VM.
	// Must be no greater than the number of CPUs on the host.
	// When unset (0), the admission controller fills in the default count.
	// +optional
	CPUs int `json:"cpus,omitempty"`
	// memory is the amount of RAM to allocate to the VM, as a Kubernetes
	// resource quantity (e.g. "4Gi", "2048Mi"). Must be at least 2Gi and no
	// greater than the total memory on the host. When unset, the admission
	// controller fills in the default amount.
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
}

// ContainerEngineSpec defines the desired container engine configuration.
type ContainerEngineSpec struct {
	// name specifies the container engine to use.
	// Valid values are "moby" (Docker-compatible) and "containerd".
	// +kubebuilder:validation:Enum=moby;containerd
	// +kubebuilder:default=moby
	Name string `json:"name"`
}

// KubernetesSpec defines the desired Kubernetes configuration.
type KubernetesSpec struct {
	// enabled specifies whether Kubernetes should be enabled in the VM.
	Enabled bool `json:"enabled"`
	// version is the Kubernetes version to use (e.g. "1.32.2").
	// +optional
	Version string `json:"version,omitempty"`
}

// ApplicationSpec defines settings for the Rancher Desktop App (the Electron
// frontend).  RDD generally does not do anything with these.
type ApplicationSpec struct {
	// updates specifies application update settings.
	// +optional
	// +kubebuilder:default={enabled:true}
	Updates ApplicationUpdatesSpec `json:"updates,omitempty"`
	// addPath controls whether and where the Rancher Desktop bin directory
	// (~/.rd<suffix>/bin) is added to the user's PATH, by editing their shell
	// startup files (Unix) or the user Environment (Windows):
	//   - "front":  prepend, so the bin directory wins   (bin:$PATH)
	//   - "back":   append,  so existing PATH entries win ($PATH:bin)
	//   - "manual": leave PATH alone; remove any lines a previous front/back added.
	// It defaults to "manual" so CLI-created instances don't touch shell startup
	// files unasked; the GUI sends "front" when the user opts in.
	//
	// On Windows the user Environment Path is a plain semicolon-separated list
	// with nowhere to fence our entry, so under "manual" we can't tell an entry
	// we added from one the user typed. We therefore remove the bin directory
	// only when it sits at the very front or back of Path (the two spots
	// front/back would have written) and leave a copy the user placed in the
	// middle alone. The trade-off: an entry parked at either end is treated as
	// ours and removed, so a user who wants the bin directory at the front or
	// back on Windows must use "front"/"back", not place it there under
	// "manual". Unix files use start/end markers, so only our own block is
	// touched regardless of position.
	// +optional
	// +kubebuilder:validation:Enum=front;back;manual
	// +kubebuilder:default=manual
	AddPath AddPathStrategy `json:"addPath,omitempty"`
	// locale is the language/locale to use for the Rancher Desktop App.
	// +optional
	// +kubebuilder:default="en-us"
	Locale string `json:"locale,omitempty"`
}

// AddPathStrategy selects whether and where the Rancher Desktop bin directory is
// added to the user's PATH.
type AddPathStrategy string

const (
	// AddPathFront prepends the bin directory (bin:$PATH).
	AddPathFront AddPathStrategy = "front"
	// AddPathBack appends the bin directory ($PATH:bin).
	AddPathBack AddPathStrategy = "back"
	// AddPathManual leaves PATH unmanaged.
	AddPathManual AddPathStrategy = "manual"
)

// ApplicationUpdatesSpec defines settings for the Rancher Desktop App's update
// mechanism.  RDD generally does not do anything with these.
type ApplicationUpdatesSpec struct {
	// enabled specifies whether the application should check for updates.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`
}

// AppSpec defines the desired state of App.
type AppSpec struct {
	// running specifies whether the VM should be running.
	Running bool `json:"running"`
	// namespace where this cluster-scoped App resource creates and manages its
	// owned namespaced resources (e.g., rancher-desktop).
	// Defaults to "default" if not specified.
	// This field is immutable after creation: changing it would orphan existing
	// owned resources (LimaVM, ConfigMaps) in the original namespace.
	// +optional
	// +kubebuilder:default="default"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.namespace is immutable"
	Namespace string `json:"namespace,omitempty"`
	// containerEngine specifies the container engine configuration.
	// +optional
	// +kubebuilder:default={name:"moby"}
	ContainerEngine ContainerEngineSpec `json:"containerEngine,omitempty"`
	// kubernetes specifies the Kubernetes configuration.
	// +optional
	Kubernetes KubernetesSpec `json:"kubernetes,omitempty"`
	// virtualMachine specifies the VM resource allocation (CPUs and memory).
	// +optional
	VirtualMachine VirtualMachineSpec `json:"virtualMachine,omitempty"`
	// application specifies the settings for the Rancher Desktop Electron frontend.
	// +optional
	// +kubebuilder:default={updates:{enabled:true}}
	Application ApplicationSpec `json:"application,omitempty"`
}

// AppStatus defines the observed state of App.
type AppStatus struct {
	// kubernetesPort is the intended port for the Kubernetes API server.
	// AppReconciler calls ResolvePort to find a free port, closes the
	// listener, then persists this value. Lima's identity port-forward rule
	// (guestPortRange:[1,65535] → hostPortRange:[0,0]) later binds the same
	// port on the host.
	//
	// The window between ResolvePort releasing the port and Lima binding it
	// spans VM boot, provisioning, and k3s install — minutes on a cold
	// start. If another process claims the port during that window, Lima
	// logs "failed to set up forwarding tcp port" and kubectl gets
	// connection refused; the LimaVM still reports Running. A future
	// improvement would keep the listener open until Lima is ready to bind,
	// or read the bound host port from Lima state instead of storing an
	// intent.
	// +optional
	KubernetesPort int `json:"kubernetesPort,omitempty"`
	// supportsNamespaces reports whether the selected container engine
	// scopes containers and images into namespaces: true for containerd,
	// false for moby. The engine controller writes it together with the
	// ContainerEngineReady condition; the field is absent until that
	// first write, so absence means unknown. It is also false whenever
	// that condition's reason is NotApplicable, because a backend that
	// mirrors nothing offers no namespaces to choose from.
	// +optional
	SupportsNamespaces *bool `json:"supportsNamespaces,omitempty"`
	// conditions represent the current state of the App resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,path=apps,categories="all"
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'app'",message="App resource must be named 'app'"

// App is the Schema for the apps API.
// This is a cluster-scoped singleton resource - only one instance named 'app' is allowed.
type App struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AppSpec   `json:"spec,omitempty"`
	Status AppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AppList contains a list of App.
type AppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []App `json:"items"`
}

func init() {
	registerTypes(&App{}, &AppList{})
}

// GetResourceNamespace implements the base.ResourceNamespace interface.
// It returns the namespace where this cluster-scoped App resource creates
// and manages its owned namespaced resources.
func (a *App) GetResourceNamespace() string {
	if a.Spec.Namespace != "" {
		return a.Spec.Namespace
	}
	return metav1.NamespaceDefault
}
