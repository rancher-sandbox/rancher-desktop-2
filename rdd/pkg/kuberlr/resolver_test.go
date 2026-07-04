// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kuberlr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"gotest.tools/v3/assert"
)

func TestAcceptable(t *testing.T) {
	v := func(major, minor uint64) semver.Version {
		return semver.Version{Major: major, Minor: minor}
	}
	// EKS/GKE/AKS rows verify ParseTolerant accepts vendor build suffixes
	// and compatible reads only the leading semver fields.
	mustParse := func(s string) semver.Version {
		ver, err := semver.ParseTolerant(s)
		assert.NilError(t, err, "ParseTolerant(%q)", s)
		return ver
	}
	type testCase struct {
		name   string
		client semver.Version
		server semver.Version
		want   bool
	}
	cases := []testCase{
		{"same minor", v(1, 30), v(1, 30), true},
		{"client one ahead", v(1, 31), v(1, 30), true},
		{"client one behind", v(1, 29), v(1, 30), false},
		{"client two ahead", v(1, 32), v(1, 30), false},
		{"client two behind", v(1, 28), v(1, 30), false},
		{"server at zero, client at zero", v(1, 0), v(1, 0), true},
		{"server at zero, client at one", v(1, 1), v(1, 0), true},
		{"patch differences ignored", semver.Version{Major: 1, Minor: 30, Patch: 99}, semver.Version{Major: 1, Minor: 30, Patch: 0}, true},
		{"different major rules out", v(1, 30), semver.Version{Major: 2, Minor: 30}, false},
		{"EKS suffix server same minor", v(1, 30), mustParse("v1.30.0-eks-1234"), true},
		{"GKE suffix server client one ahead", v(1, 31), mustParse("v1.30.7-gke.500"), true},
		{"AKS suffix server client behind", v(1, 30), mustParse("v1.32.0-aks.1"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, acceptable(tc.client, tc.server), tc.want)
		})
	}
}

func TestBestCached(t *testing.T) {
	t.Setenv(envCacheDir, t.TempDir())
	server := semver.Version{Major: 1, Minor: 34, Patch: 1}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dir := CacheDir()
	place := func(name string) {
		t.Helper()
		assert.NilError(t, os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755))
	}
	assertMiss := func(comment string) {
		t.Helper()
		path, ok := bestCached(server)
		assert.Assert(t, !ok, "%s: unexpected hit %q", comment, path)
	}
	assertHit := func(name string) {
		t.Helper()
		path, ok := bestCached(server)
		assert.Assert(t, ok, "expected a hit for %q", name)
		assert.Equal(t, path, filepath.Join(dir, name+exe))
	}

	assertMiss("cache directory does not exist yet")

	assert.NilError(t, os.MkdirAll(dir, 0o755))
	place(".kubectl-tmp123" + exe)
	place("README")
	assert.NilError(t, os.Mkdir(filepath.Join(dir, "kubectl-v1.34.9"+exe), 0o755))
	assertMiss("temp files, foreign names, and directories")

	place("kubectl-v1.33.9" + exe)
	place("kubectl-v1.36.0" + exe)
	assertMiss("one minor older and two minors newer")

	place("kubectl-v1.34.0" + exe)
	assertHit("kubectl-v1.34.0")

	place("kubectl-v1.35.2" + exe)
	place("kubectl-v1.34.7" + exe)
	assertHit("kubectl-v1.35.2")
}

func TestLatestPatch(t *testing.T) {
	server := semver.Version{Major: 1, Minor: 34, Patch: 0}
	cases := []struct {
		name    string
		status  int
		body    string
		want    string // expected version; empty means an error containing errPart
		errPart string
	}{
		{"trailing newline", http.StatusOK, "v1.34.9\n", "1.34.9", ""},
		{"bare version", http.StatusOK, "1.34.10", "1.34.10", ""},
		{"wrong minor", http.StatusOK, "v1.35.1\n", "", "not a v1.34 release"},
		{"garbage", http.StatusOK, "<html>oops</html>", "", "malformed version marker"},
		{"missing marker", http.StatusNotFound, "", "", "404"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/release/stable-1.34.txt", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()
			t.Setenv(envKubeMirror, ts.URL)

			got, err := latestPatch(context.Background(), server)
			if tc.want == "" {
				assert.ErrorContains(t, err, tc.errPart)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, got.String(), tc.want)
			}
		})
	}
}

func TestCachedPath(t *testing.T) {
	v := semver.Version{Major: 1, Minor: 30, Patch: 5}
	got := cachedPath(v)
	wantSuffix := "kubectl-v1.30.5"
	if runtime.GOOS == "windows" {
		wantSuffix += ".exe"
	}
	assert.Assert(t, strings.HasSuffix(got, wantSuffix), "cachedPath = %q, want suffix %q", got, wantSuffix)
	assert.Assert(t, strings.HasPrefix(got, CacheDir()), "cachedPath = %q, want prefix %q", got, CacheDir())
}

func TestParseKubeConfigFlags(t *testing.T) {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	derefBool := func(p *bool) bool {
		if p == nil {
			return false
		}
		return *p
	}

	type want struct {
		kubeconfig string
		context    string
		server     string
		cluster    string
		user       string
		token      string
		caFile     string
		certFile   string
		keyFile    string
		tlsName    string
		insecure   bool
		username   string
		password   string
		namespace  string
	}
	cases := []struct {
		name string
		args []string
		want want
	}{
		{"empty", nil, want{}},
		{"unrelated args", []string{"get", "pods"}, want{}},
		{"--kubeconfig spaced", []string{"--kubeconfig", "/k", "get", "pods"}, want{kubeconfig: "/k"}},
		{"--kubeconfig equals", []string{"--kubeconfig=/k", "get", "pods"}, want{kubeconfig: "/k"}},
		{"--context spaced", []string{"--context", "c", "get", "pods"}, want{context: "c"}},
		{"--context equals", []string{"--context=c", "get", "pods"}, want{context: "c"}},
		{"--server spaced", []string{"--server", "https://x:6443", "get"}, want{server: "https://x:6443"}},
		{"--server equals", []string{"--server=https://x:6443", "get"}, want{server: "https://x:6443"}},
		{"-s alias spaced", []string{"-s", "https://x:6443", "get"}, want{server: "https://x:6443"}},
		{"-s alias equals", []string{"-s=https://x:6443", "get"}, want{server: "https://x:6443"}},
		{"--cluster", []string{"--cluster=prod", "get"}, want{cluster: "prod"}},
		{"--user", []string{"--user=alice", "get"}, want{user: "alice"}},
		{"--token", []string{"--token=eyJabc", "get"}, want{token: "eyJabc"}},
		{"--certificate-authority", []string{"--certificate-authority=/ca.crt", "get"}, want{caFile: "/ca.crt"}},
		{"--client-certificate", []string{"--client-certificate=/c.crt", "get"}, want{certFile: "/c.crt"}},
		{"--client-key", []string{"--client-key=/c.key", "get"}, want{keyFile: "/c.key"}},
		{"--tls-server-name", []string{"--tls-server-name=api.example", "get"}, want{tlsName: "api.example"}},
		{"--insecure-skip-tls-verify bare", []string{"--insecure-skip-tls-verify", "get"}, want{insecure: true}},
		{"--insecure-skip-tls-verify=true", []string{"--insecure-skip-tls-verify=true", "get"}, want{insecure: true}},
		{"--namespace spaced", []string{"--namespace", "kube-system", "get"}, want{namespace: "kube-system"}},
		{"-n alias", []string{"-n", "kube-system", "get"}, want{namespace: "kube-system"}},
		{"all auth/tls together", []string{"--server=https://x", "--token=tk", "--certificate-authority=/ca", "--insecure-skip-tls-verify", "get"}, want{server: "https://x", token: "tk", caFile: "/ca", insecure: true}},
		{"later flag wins", []string{"--context=a", "--context=b"}, want{context: "b"}},
		{"stops at --", []string{"exec", "pod", "--", "tool", "--kubeconfig=/other", "--server=https://other"}, want{}},
		{"flags before -- still parse", []string{"--kubeconfig=/k", "--server=https://x", "exec", "pod", "--", "tool", "--context=other"}, want{kubeconfig: "/k", server: "https://x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cf, err := parseKubeConfigFlags(tc.args)
			assert.NilError(t, err)
			actual := want{
				kubeconfig: deref(cf.KubeConfig),
				context:    deref(cf.Context),
				server:     deref(cf.APIServer),
				cluster:    deref(cf.ClusterName),
				user:       deref(cf.AuthInfoName),
				token:      deref(cf.BearerToken),
				caFile:     deref(cf.CAFile),
				certFile:   deref(cf.CertFile),
				keyFile:    deref(cf.KeyFile),
				tlsName:    deref(cf.TLSServerName),
				insecure:   derefBool(cf.Insecure),
				username:   deref(cf.Username),
				password:   deref(cf.Password),
				namespace:  deref(cf.Namespace),
			}
			assert.Equal(t, actual, tc.want)
		})
	}
}

// Locks in parseKubeConfigFlags' malformed-flag behavior (see its doc).
func TestParseKubeConfigFlagsRejectsMalformedFlag(t *testing.T) {
	_, err := parseKubeConfigFlags([]string{"get", "--server"})
	assert.Assert(t, err != nil, "expected a parse error for --server without a value")
}

func TestIsClientOnly(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, true},
		{"--help alone", []string{"--help"}, true},
		{"-h alone", []string{"-h"}, true},
		{"--help after subcommand", []string{"get", "pods", "--help"}, true},
		{"--help=true alone", []string{"--help=true"}, true},
		{"--help=1 after subcommand", []string{"get", "pods", "--help=1"}, true},
		{"-h=true alone", []string{"-h=true"}, true},
		{"--help=false probes", []string{"--help=false", "get", "pods"}, false},
		{"-h=0 probes", []string{"-h=0", "get", "pods"}, false},
		{"--help=garbage stops parse, no positionals", []string{"--help=garbage", "get", "pods"}, true},
		{"version --client", []string{"version", "--client"}, true},
		{"version --client with --output", []string{"version", "--client", "--output=json"}, true},
		{"version --client=true", []string{"version", "--client=true"}, true},
		{"version --client=false", []string{"version", "--client=false"}, false},
		{"version --client=1", []string{"version", "--client=1"}, true},
		{"version --client then --client=false (last wins)", []string{"version", "--client", "--client=false"}, false},
		{"version --client=false then --client (last wins)", []string{"version", "--client=false", "--client"}, true},
		{"version --client=garbage stays false", []string{"version", "--client=garbage"}, false},
		{"version without --client", []string{"version"}, false},
		{"completion bash", []string{"completion", "bash"}, true},
		{"config view", []string{"config", "view"}, true},
		{"config get-contexts", []string{"config", "get-contexts"}, true},
		{"kustomize bare", []string{"kustomize"}, true},
		{"kustomize with dir", []string{"kustomize", "./manifests"}, true},
		{"kustomize with --enable-helm", []string{"kustomize", "./m", "--enable-helm"}, true},
		{"plugin bare", []string{"plugin"}, true},
		{"plugin list", []string{"plugin", "list"}, true},
		{"options", []string{"options"}, true},
		{"help subcommand", []string{"help", "get"}, true},
		{"get pods", []string{"get", "pods"}, false},
		{"apply -f", []string{"apply", "-f", "manifest.yaml"}, false},
		{"with kubeconfig flag spaced", []string{"--kubeconfig", "/k", "completion", "bash"}, true},
		{"with kubeconfig flag equals", []string{"--kubeconfig=/k", "completion", "bash"}, true},
		{"with --server before subcommand", []string{"--server", "https://x", "config", "view"}, true},
		{"--server URL must not be read as subcommand", []string{"--server", "completion", "get", "pods"}, false},
		{"-n namespace before subcommand", []string{"-n", "kube-system", "get", "pods"}, false},
		{"-n with config", []string{"-n", "ns", "config", "view"}, true},
		{"--as-user-extra spaced before subcommand", []string{"--as-user-extra", "k=v", "config", "view"}, true},
		{"--as-user-extra equals before subcommand", []string{"--as-user-extra=k=v", "config", "view"}, true},
		{"unknown long flag swallows next non-flag arg as assumed value", []string{"--unknown", "config", "view"}, false},
		{"stops at --", []string{"exec", "pod", "--", "completion"}, false},
		{"-- before subcommand still finds the subcommand", []string{"--", "get", "pods"}, false},
		{"unknown --v spaced rides UnknownFlags path", []string{"--v", "4", "config", "view"}, true},
		{"unknown --v=N equals form rides UnknownFlags path", []string{"--v=4", "config", "view"}, true},
		{"unknown --vmodule spaced rides UnknownFlags path", []string{"--vmodule", "foo=2", "config", "view"}, true},
		{"--warnings-as-errors bare bool", []string{"--warnings-as-errors", "config", "view"}, true},
		{"--warnings-as-errors=true", []string{"--warnings-as-errors=true", "config", "view"}, true},
		{"--match-server-version bare bool", []string{"--match-server-version", "config", "view"}, true},
		{"--match-server-version=true", []string{"--match-server-version=true", "config", "view"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, isClientOnly(tc.args), tc.want)
		})
	}
}

// TestResolveFallsBackToEmbeddedWhenMarkerUnavailable locks in the
// final fallback: no acceptable cache entry and no reachable version
// marker resolve to the embedded kubectl, with a warning.
func TestResolveFallsBackToEmbeddedWhenMarkerUnavailable(t *testing.T) {
	orig := embeddedVersion
	t.Cleanup(func() { embeddedVersion = orig })
	embeddedVersion = func() (semver.Version, error) {
		return semver.Version{Major: 1, Minor: 36}, nil
	}

	// One server plays apiserver and mirror, like the BATS fixture: it
	// answers /version with a minor the embedded kubectl does not
	// satisfy, and 404s the version-marker request.
	mux := http.NewServeMux()
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"major":"1","minor":"30","gitVersion":"v1.30.0"}`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	config := fmt.Sprintf(`
apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: %s
contexts:
- name: fake
  context:
    cluster: fake
    user: ""
current-context: fake
`, ts.URL)
	assert.NilError(t, os.WriteFile(kubeconfig, []byte(config), 0o600))
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv(envKubeMirror, ts.URL)
	t.Setenv(envCacheDir, t.TempDir())

	// Capture the warning: it must be asserted, not printed into the
	// test output.
	var logBuf bytes.Buffer
	origOut := logrus.StandardLogger().Out
	logrus.StandardLogger().SetOutput(&logBuf)
	t.Cleanup(func() { logrus.StandardLogger().SetOutput(origOut) })

	path, err := Resolve(context.Background(), []string{"get", "pods"})
	assert.NilError(t, err)
	assert.Equal(t, path, "")
	assert.Assert(t, strings.Contains(logBuf.String(), "using embedded kubectl"),
		"expected fallback warning, got: %q", logBuf.String())
}

// TestResolveEmbeddedVersionUnparseable locks in Resolve's dev-build
// fall-through: an unparseable embeddedVersion returns ("", nil).
func TestResolveEmbeddedVersionUnparseable(t *testing.T) {
	orig := embeddedVersion
	t.Cleanup(func() { embeddedVersion = orig })
	embeddedVersion = func() (semver.Version, error) {
		return semver.Version{}, errors.New("test: unparseable embedded version")
	}

	// Args that would otherwise reach the cluster probe (not client-only).
	path, err := Resolve(context.Background(), []string{"get", "pods"})
	assert.NilError(t, err)
	assert.Equal(t, path, "")
}

// TestResolveSkipsWhenRecursionGuardSet locks in the envSkipResolver
// short-circuit, asserted via embeddedVersion never being called.
func TestResolveSkipsWhenRecursionGuardSet(t *testing.T) {
	t.Setenv(envSkipResolver, "1")
	orig := embeddedVersion
	t.Cleanup(func() { embeddedVersion = orig })
	embeddedVersionCalled := false
	embeddedVersion = func() (semver.Version, error) {
		embeddedVersionCalled = true
		return semver.Version{}, nil
	}

	// Args that would otherwise reach the cluster probe.
	path, err := Resolve(context.Background(), []string{"get", "pods"})
	assert.NilError(t, err)
	assert.Equal(t, path, "")
	assert.Assert(t, !embeddedVersionCalled, "Resolve reached embeddedVersion despite envSkipResolver")
}
