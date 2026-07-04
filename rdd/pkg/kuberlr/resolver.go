// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kuberlr

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	componentbasever "k8s.io/component-base/version"
)

// envSkipResolver tells Resolve to short-circuit — Exec sets it on the
// kubectl child so a re-exec'd kubectl can't recurse into resolution.
const envSkipResolver = "RDD_KUBECTL_RESOLVED"

// skipResolver is the same-process counterpart to envSkipResolver.
var skipResolver bool

// SkipResolver short-circuits Resolve for the rest of this process.
// rdd ctl calls this because its embedded apiserver always matches
// the embedded kubectl's version by construction.
func SkipResolver() {
	skipResolver = true
}

// serverProbeTimeout caps the discovery call so an unreachable cluster
// can't stall every kubectl invocation; reachable clusters answer in <100ms.
const serverProbeTimeout = 2 * time.Second

// Resolve returns the path of a kubectl acceptable for the cluster the
// user's kubeconfig points at, or "" to use the embedded kubectl.
//
// It prefers the embedded kubectl, then the highest acceptable cached
// binary, and only when both miss downloads the latest patch release
// of the server's minor. It returns "" when the probe fails for any
// reason, for client-only commands (see isClientOnly), and — with a
// warning — when the version to download cannot be determined; a
// determined version that fails to download or verify is an error.
func Resolve(ctx context.Context, args []string) (string, error) {
	if skipResolver || os.Getenv(envSkipResolver) != "" {
		return "", nil
	}
	if isClientOnly(args) {
		return "", nil
	}
	embedded, err := embeddedVersion()
	if err != nil {
		// `go run` and IDE debug builds skip the Makefile's -X flags,
		// leaving the unparseable in-tree default (v0.0.0-master+...);
		// fall through rather than break every dev invocation.
		logrus.WithError(err).Debug("kubectl resolver: embedded version not parseable; using embedded kubectl")
		return "", nil
	}
	server, ok := serverVersion(args)
	if !ok {
		return "", nil
	}
	if acceptable(embedded, server) {
		return "", nil
	}
	if path, ok := bestCached(server); ok {
		return path, nil
	}
	want, err := latestPatch(ctx, server)
	if err != nil {
		// Final fallback: rdd embeds a recent kubectl and Kubernetes
		// rarely breaks backward compatibility, so running it beats
		// failing when the mirror is unreachable or has no marker yet.
		logrus.WithError(err).Warnf("kubectl resolver: cannot determine the latest v%d.%d patch release; using embedded kubectl", server.Major, server.Minor)
		return "", nil
	}
	return ensureCached(ctx, want)
}

// acceptable reports whether a kubectl of client's version supports
// every feature of a server of server's version: same Major, and a
// Minor equal to the server's or one higher. One minor behind is
// still within the official ±1 skew, but lacks the features added in
// the server's minor, so it is rejected. Patch versions never matter.
func acceptable(client, server semver.Version) bool {
	if client.Major != server.Major {
		return false
	}
	diff := int64(client.Minor) - int64(server.Minor)
	return diff == 0 || diff == 1
}

// embeddedVersion reads the kubectl version baked in via the Makefile's
// -X flags; it's a var so tests can stub the dev-build parse failure.
var embeddedVersion = func() (semver.Version, error) {
	return semver.ParseTolerant(componentbasever.Get().GitVersion)
}

// serverVersion probes the cluster named by args. ok is false on any
// failure, all logged at debug except unparseable version (warn — a
// garbled apiserver /version response is worth surfacing by default).
func serverVersion(args []string) (semver.Version, bool) {
	cfg, err := loadClientConfig(args)
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cannot load kubeconfig; using embedded kubectl")
		return semver.Version{}, false
	}
	// Force the fast probe timeout, overriding any --request-timeout in
	// args; the kubectl child itself still honors the user's value.
	cfg.Timeout = serverProbeTimeout
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cannot build discovery client; using embedded kubectl")
		return semver.Version{}, false
	}
	info, err := client.ServerVersion()
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cluster probe failed; using embedded kubectl")
		return semver.Version{}, false
	}
	v, err := semver.ParseTolerant(info.GitVersion)
	if err != nil {
		logrus.WithError(err).Warnf("kubectl resolver: cannot parse server version %q; using embedded kubectl", info.GitVersion)
		return semver.Version{}, false
	}
	return v, true
}

// loadClientConfig builds a rest.Config from KUBECONFIG and args'
// kubectl connection flags, so the probe targets the same cluster.
func loadClientConfig(args []string) (*rest.Config, error) {
	cf, err := parseKubeConfigFlags(args)
	if err != nil {
		return nil, err
	}
	return cf.ToRawKubeConfigLoader().ClientConfig()
}

// parseKubeConfigFlags parses kubectl's connection-override flags into a
// ConfigFlags. A malformed known flag returns the parse error, so the
// caller falls through to the embedded kubectl instead of silently
// probing the default cluster; unknown flags pass through untouched.
//
// --username/--password stay unbound: binding them would make
// ToRawKubeConfigLoader prompt on stdin, hanging a background probe.
func parseKubeConfigFlags(args []string) (*genericclioptions.ConfigFlags, error) {
	fs := pflag.NewFlagSet("rdd-kubectl", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	cf := genericclioptions.NewConfigFlags(true)
	cf.AddFlags(fs)
	return cf, fs.Parse(args)
}

// clientOnlySubcommands lists kubectl subcommands that never contact a
// cluster: config/completion touch local state, kustomize renders local
// manifests, plugin lists local PATH entries, options/help print text.
var clientOnlySubcommands = map[string]bool{
	"completion": true,
	"config":     true,
	"kustomize":  true,
	"plugin":     true,
	"options":    true,
	"help":       true,
}

// isClientOnly reports whether args need no cluster probe. When unsure
// it defaults to false (probe), to avoid running a mismatched kubectl.
//
// Klog flags (--v, ...) stay unbound to avoid leaking into klog's
// process-global state. A malformed bool like --client=garbage is safe:
// it lands in the empty-args (true) branch, and kubectl rejects it too.
func isClientOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	fs := pflag.NewFlagSet("rdd-kubectl", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	cf := genericclioptions.NewConfigFlags(true)
	cf.AddFlags(fs)

	var help, client, warningsAsErrors, matchServerVersion bool
	fs.BoolVarP(&help, "help", "h", false, "")
	fs.BoolVar(&client, "client", false, "")
	fs.BoolVar(&warningsAsErrors, "warnings-as-errors", false, "")
	fs.BoolVar(&matchServerVersion, "match-server-version", false, "")

	_ = fs.Parse(args)

	if help {
		return true
	}
	positionals := fs.Args()
	if len(positionals) == 0 {
		return true
	}
	subcommand := positionals[0]
	if subcommand == "version" {
		// `kubectl version` without --client probes the server, so
		// treat it as cluster-bound.
		return client
	}
	return clientOnlySubcommands[subcommand]
}
