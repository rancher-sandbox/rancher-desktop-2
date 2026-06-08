// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
)

// Suffix returns the instance suffix from the RDD_INSTANCE environment variable, defaulting to "2".
var Suffix = sync.OnceValue(func() string {
	instance := os.Getenv("RDD_INSTANCE")
	if instance == "" {
		return "2"
	}
	return instance
})

// Index returns unique number for the control plane instance as long as the
// instance suffix is numeric and in the range from 1 to 99. Otherwise, it is
// computed based on a checksum of the suffix, and may have collisions.
// This index can be used to select unique port numbers per control plane
// (for the apiserver, or for etcd, etc).
var Index = sync.OnceValue(func() int {
	if i, err := strconv.Atoi(Suffix()); err == nil && 0 < i && i < 100 {
		return i
	}
	sum := 0
	for _, ch := range Suffix() {
		sum += int(ch)
	}
	return 100 + (sum % 100)
})

// Name returns the instance name (e.g., "rancher-desktop-2").
var Name = sync.OnceValue(func() string {
	return "rancher-desktop-" + Suffix()
})

// Dir returns the OS-specific data directory for this instance.
var Dir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not get home directory: %w", err))
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local", Name())
	case "linux":
		return filepath.Join(home, ".local", "share", Name())
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", Name())
	default:
		panic(fmt.Sprintf("platform %s not supported", runtime.GOOS))
	}
})

// LogDir returns the OS-specific log directory for this instance.
// Logs are stored separately from instance data so they can be preserved
// independently (e.g., when deleting an instance but keeping logs for debugging).
var LogDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not get home directory: %w", err))
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local", Name()+"-logs")
	case "linux":
		return filepath.Join(home, ".local", "state", Name())
	case "darwin":
		return filepath.Join(home, "Library", "Logs", Name())
	default:
		panic(fmt.Sprintf("platform %s not supported", runtime.GOOS))
	}
})

// ArgsFile returns the path to the saved service arguments file.
var ArgsFile = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "args.json")
})

// Config returns the path to the rdd apiserver's kubeconfig.
// `rdd ctl` pass-throughs export this path as KUBECONFIG.
var Config = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "config.yaml")
})

// K3sConfig returns the path where the in-VM k3s kubeconfig is mirrored
// by the Lima probe. Distinct from Config() so kubectl pass-throughs
// keep talking to the rdd apiserver, not k3s.
var K3sConfig = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "k3s.yaml")
})

// PIDFile returns the path to the service PID file.
var PIDFile = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "rdd.pid")
})

// TLSDir returns the path to the TLS certificate directory.
var TLSDir = sync.OnceValue(func() string {
	return filepath.Join(Dir(), "tls")
})

// ShortDir returns the short directory path for this instance (e.g., ~/.rd2).
// This is distinct from Dir() which returns the service directory.
// Lima uses ShortDir() because of socket name length constraints.
// See docs/design/cmd_service.md for directory documentation.
var ShortDir = sync.OnceValue(func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not get home directory: %w", err))
	}
	return filepath.Join(home, ".rd"+Suffix())
})

// LimaHome returns the LIMA_HOME directory for this instance (e.g., ~/.rd2/lima).
// Lima uses this directory instead of the service directory because of socket name
// length constraints.
var LimaHome = sync.OnceValue(func() string {
	return filepath.Join(ShortDir(), "lima")
})

// KubeConfig returns the path to the instance's standalone kubeconfig
// (e.g., ~/.rd2/kube.config). It holds only the rancher-desktop-{instance}
// context and is published alongside the ~/.kube/config merge, so rdd run
// can point KUBECONFIG at the instance without copying credentials itself.
var KubeConfig = sync.OnceValue(func() string {
	return filepath.Join(ShortDir(), "kube.config")
})

// DockerSocket returns the path to the Docker socket for this instance
// (e.g., ~/.rd2/docker.sock). This is the host-side socket that Lima
// port-forwards from the guest's /var/run/docker.sock.
// On Windows, returns the named pipe path (\\.\pipe\docker_engine).
var DockerSocket = sync.OnceValue(func() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\docker_engine`
	}
	return filepath.Join(ShortDir(), "docker.sock")
})

// DockerEndpoint returns the full Docker endpoint URL for this instance.
// On Windows this is a named-pipe URL (npipe:////./pipe/docker_engine);
// on Unix it is a unix-socket URL (unix:///path/to/docker.sock).
var DockerEndpoint = sync.OnceValue(func() string {
	if runtime.GOOS == "windows" {
		return `npipe:////./pipe/docker_engine`
	}
	return "unix://" + DockerSocket()
})
