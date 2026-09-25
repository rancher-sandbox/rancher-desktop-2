// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"encoding/json"
	"runtime"
)

// PlatformSpecificManifest represents a value that can differ based on the platform
// (Darwin, Linux, Windows).
type PlatformSpecificManifest[T any] struct {
	Darwin  T `json:"darwin,omitempty"`
	Linux   T `json:"linux,omitempty"`
	Windows T `json:"windows,omitempty"`
}

// Get the value for the current platform.
func (p *PlatformSpecificManifest[T]) Get() T {
	switch runtime.GOOS {
	case "darwin":
		return p.Darwin
	case "linux":
		return p.Linux
	case "windows":
		return p.Windows
	default:
		var zero T
		return zero
	}
}

// ExtensionManifest represents the manifest of an extension.
type ExtensionManifest struct {
	// Icon for the extension, as a path in the image.
	Icon string `json:"icon,omitempty"`
	// UI endpoints.  Currently only "dashboard-tab" is supported.
	UI map[string]struct {
		// The title of the UI, as shown in the side bar.
		Title string `json:"title"`
		// Root of the directory inside the image holding the UI files.
		Root string `json:"root"`
		// The initial HTML page to load, relative to Root.
		Src string `json:"src"`
		// Information on the backend to expose.
		Backend struct {
			// The name of the socket, as found in vm.exposes.socket.
			Socket string `json:"socket"`
		}
	} `json:"ui,omitempty"`
	// Containers to run.
	VM struct {
		// The image containing the VM; exclusive with ComposeFile.
		Image string `json:"image"`
		// The Compose file defining the VM; exclusive with Image.
		ComposeFile string `json:"composefile"`
		// THings to expose to the UI.
		Exposes struct {
			// Path to a Unix socket to expose; this is in `/run/guest-services/`.
			Socket string `json:"socket"`
		} `json:"exposes"`
	} `json:"vm,omitempty"`
	Host struct {
		// Files to copy to the host.
		Binaries []PlatformSpecificManifest[[]struct {
			Path string `json:"path"`
		}] `json:"binaries"`
		// Rancher Desktop extension: this will be run after the extension is
		// installed (possibly as an upgrade, or on restart).  This file should be
		// listed in Binaries.  Errors will be ignored.
		InstallScript PlatformSpecificManifest[StringOrStringSlice] `json:"x-rd-install,omitempty"`
		// Rancher Desktop extension: this will be run before the extension is
		// uninstalled (possible as an upgrade).  This file should be listed in
		// Binaries.  Errors will be ignored.
		UninstallScript PlatformSpecificManifest[StringOrStringSlice] `json:"x-rd-uninstall,omitempty"`
		// Rancher Desktop extension: this will be executed when the application
		// quits.  The application may exit before the process completes.  It is not
		// defined what the container engine / Kubernetes cluster may be doing at
		// the time this is called.
		ShutdownScript PlatformSpecificManifest[StringOrStringSlice] `json:"x-rd-shutdown,omitempty"`
	} `json:"host"`
}

// StringOrStringSlice represents a value that is marshalled in JSON as either a
// single string or a slice of strings.
type StringOrStringSlice []string

// UnmarshalJSON implements the json.Unmarshaler interface for StringOrStringSlice.
func (s *StringOrStringSlice) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = StringOrStringSlice{single}
		return nil
	}
	var slice []string
	if err := json.Unmarshal(data, &slice); err != nil {
		return err
	}
	*s = StringOrStringSlice(slice)
	return nil
}
