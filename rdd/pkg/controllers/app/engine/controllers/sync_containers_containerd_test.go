// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package controllers

import (
	"strings"
	"syscall"
	"testing"

	containerdclient "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"gotest.tools/v3/assert"

	"k8s.io/apimachinery/pkg/util/validation"

	containersv1alpha1 "github.com/rancher-sandbox/rancher-desktop-daemon/pkg/apis/containers/v1alpha1"
)

func TestContainerdMirrorName(t *testing.T) {
	const hexID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("keeps a DNS1123 id as the mirror name", func(t *testing.T) {
		assert.Equal(t, containerdMirrorName("default", hexID), hexID)
	})

	t.Run("hashes an id that is not a valid object name", func(t *testing.T) {
		got := containerdMirrorName("default", "Has_Upper")
		assert.Assert(t, strings.HasPrefix(got, "ctr-"))
		assert.Equal(t, len(validation.IsDNS1123Subdomain(got)), 0)
	})

	t.Run("separates the namespace from the id", func(t *testing.T) {
		// Concatenated without a separator both of these are "abB_c", so this
		// fails if the separator is ever dropped from the hashed input.
		assert.Assert(t, containerdMirrorName("a", "bB_c") != containerdMirrorName("ab", "B_c"))
	})

	t.Run("is stable across calls", func(t *testing.T) {
		assert.Equal(t, containerdMirrorName("ns", "A_b"), containerdMirrorName("ns", "A_b"))
	})
}

func TestContainerdImageMirrorName(t *testing.T) {
	t.Run("always hashes, since refs are never valid object names", func(t *testing.T) {
		got := containerdImageMirrorName("default", "docker.io/library/busybox:latest")
		assert.Assert(t, strings.HasPrefix(got, "img-"))
		assert.Equal(t, len(validation.IsDNS1123Subdomain(got)), 0)
	})

	t.Run("distinguishes the same ref in different namespaces", func(t *testing.T) {
		ref := "docker.io/library/busybox:latest"
		assert.Assert(t, containerdImageMirrorName("default", ref) != containerdImageMirrorName("k8s.io", ref))
	})

	t.Run("separates the namespace from the ref", func(t *testing.T) {
		// Concatenated without a separator both of these are "abx".
		assert.Assert(t, containerdImageMirrorName("a", "bx") != containerdImageMirrorName("ab", "x"))
	})

	t.Run("is stable across calls", func(t *testing.T) {
		ref := "docker.io/library/busybox:latest"
		assert.Equal(t, containerdImageMirrorName("ns", ref), containerdImageMirrorName("ns", ref))
	})
}

func TestMapContainerdProcessStatus(t *testing.T) {
	tests := []struct {
		input containerdclient.ProcessStatus
		want  containersv1alpha1.ContainerStatusValue
	}{
		{containerdclient.Created, containersv1alpha1.ContainerStatusCreated},
		{containerdclient.Running, containersv1alpha1.ContainerStatusRunning},
		{containerdclient.Stopped, containersv1alpha1.ContainerStatusExited},
		{containerdclient.Paused, containersv1alpha1.ContainerStatusPaused},
		{containerdclient.Pausing, containersv1alpha1.ContainerStatusPausing},
		// A status containerd adds later must not fail CRD validation.
		{containerdclient.ProcessStatus("something-new"), containersv1alpha1.ContainerStatusUnknown},
	}
	for _, tc := range tests {
		t.Run(string(tc.input), func(t *testing.T) {
			assert.Equal(t, mapContainerdProcessStatus(tc.input), tc.want)
		})
	}
}

// rawAny is a typeurl.Any carrying pre-marshalled bytes, which is all
// containerdProcessArgs reads off the record.
type rawAny []byte

//nolint:revive,staticcheck // typeurl.Any declares the method with this name.
func (r rawAny) GetTypeUrl() string { return "test" }
func (r rawAny) GetValue() []byte   { return r }

// specRecord wraps a runtime-spec JSON document the way containerd stores it
// on the container record. Feeding real JSON rather than a marshalled struct
// checks that the production unmarshal target matches the on-disk field names.
func specRecord(spec string) containers.Container {
	return containers.Container{ID: "test", Spec: rawAny(spec)}
}

func TestContainerdProcessArgs(t *testing.T) {
	t.Run("returns the full command line", func(t *testing.T) {
		got, err := containerdProcessArgs(
			specRecord(`{"process":{"args":["/bin/sh","-c","echo hi"]}}`))
		assert.NilError(t, err)
		assert.DeepEqual(t, got, []string{"/bin/sh", "-c", "echo hi"})
	})

	t.Run("reports a record with no spec", func(t *testing.T) {
		_, err := containerdProcessArgs(containers.Container{ID: "test"})
		assert.ErrorContains(t, err, "no runtime spec")
	})

	t.Run("reports a spec with no process", func(t *testing.T) {
		_, err := containerdProcessArgs(specRecord(`{}`))
		assert.ErrorContains(t, err, "no process")
	})

	t.Run("reports an unparsable spec", func(t *testing.T) {
		_, err := containerdProcessArgs(specRecord("not json"))
		assert.ErrorContains(t, err, "failed to unmarshal runtime spec")
	})
}

// portsRecord wraps a nerdctl/ports label the way nerdctl writes it.
func portsRecord(label string) containers.Container {
	return containers.Container{ID: "test", Labels: map[string]string{"nerdctl/ports": label}}
}

func TestContainerdPorts(t *testing.T) {
	t.Run("no label yields no ports", func(t *testing.T) {
		got, err := containerdPorts(containers.Container{ID: "test"})
		assert.NilError(t, err)
		assert.Equal(t, len(got), 0)
	})

	t.Run("names a port the way Docker does", func(t *testing.T) {
		got, err := containerdPorts(portsRecord(
			`[{"HostPort":8080,"ContainerPort":80,"Protocol":"tcp","HostIP":"0.0.0.0"}]`))
		assert.NilError(t, err)
		assert.Equal(t, len(got), 1)
		assert.Equal(t, *got[0].Name, "80/tcp")
		assert.Equal(t, len(got[0].Bindings), 1)
		assert.Equal(t, *got[0].Bindings[0].HostPort, "8080")
		assert.Equal(t, *got[0].Bindings[0].HostIP, "0.0.0.0")
	})

	t.Run("groups dual-stack bindings under one name", func(t *testing.T) {
		got, err := containerdPorts(portsRecord(
			`[{"HostPort":8080,"ContainerPort":80,"Protocol":"tcp","HostIP":"::"},
			  {"HostPort":8080,"ContainerPort":80,"Protocol":"tcp","HostIP":"0.0.0.0"}]`))
		assert.NilError(t, err)
		assert.Equal(t, len(got), 1)
		assert.Equal(t, len(got[0].Bindings), 2)
		// Sorted by HostIP, so the order nerdctl emitted does not leak through.
		assert.Equal(t, *got[0].Bindings[0].HostIP, "0.0.0.0")
		assert.Equal(t, *got[0].Bindings[1].HostIP, "::")
	})

	t.Run("sorts names so repeated syncs produce the same object", func(t *testing.T) {
		got, err := containerdPorts(portsRecord(
			`[{"HostPort":9,"ContainerPort":90,"Protocol":"udp","HostIP":"0.0.0.0"},
			  {"HostPort":8,"ContainerPort":80,"Protocol":"tcp","HostIP":"0.0.0.0"}]`))
		assert.NilError(t, err)
		assert.DeepEqual(t, []string{*got[0].Name, *got[1].Name}, []string{"80/tcp", "90/udp"})
	})

	t.Run("reports an unparsable label", func(t *testing.T) {
		_, err := containerdPorts(portsRecord("not json"))
		assert.ErrorContains(t, err, "failed to unmarshal nerdctl/ports")
	})
}

func TestParseLinuxSignal(t *testing.T) {
	// The numbers are the guest's, not the host's. On macOS syscall.SIGUSR1
	// is 30, so a test that compared against the host constants would pass
	// on Linux and hide the bug everywhere else.
	tests := []struct {
		raw  string
		want syscall.Signal
	}{
		{"SIGTERM", 15},
		{"SIGUSR1", 10},
		{"SIGUSR2", 12},
		{"SIGQUIT", 3},
		{"sigusr1", 10},
		{"USR1", 10},
		{"9", 9},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, ok := parseLinuxSignal(tc.raw)
			assert.Assert(t, ok)
			assert.Equal(t, got, tc.want)
		})
	}

	for _, raw := range []string{"", "SIGNOPE", "0", "64", "-1"} {
		t.Run("rejects "+raw, func(t *testing.T) {
			_, ok := parseLinuxSignal(raw)
			assert.Assert(t, !ok)
		})
	}
}
