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

// envSkipResolver tells Resolve to short-circuit. Exec sets it on the
// kubectl child process so a downloaded kubectl that re-execs us through
// a shim cannot recurse back into version resolution.
const envSkipResolver = "RDD_KUBECTL_RESOLVED"

// skipResolver short-circuits Resolve for the rest of this process.
// Same-process toggle; envSkipResolver covers the cross-process recursion
// guard that Exec sets on the kubectl child.
var skipResolver bool

// SkipResolver short-circuits Resolve for the rest of this process.
// rdd ctl calls this before kubectlAction because it always targets
// the embedded apiserver, whose version matches the embedded kubectl
// by construction — the probe would always fall through anyway.
func SkipResolver() {
	skipResolver = true
}

// serverProbeTimeout caps the discovery call. A reachable cluster answers
// in under 100 ms; an unreachable one would otherwise stall every kubectl
// invocation.
const serverProbeTimeout = 2 * time.Second

// Resolve returns the path of a kubectl compatible with the cluster the
// user's kubeconfig points at, or "" to use the embedded kubectl. It
// binds kubectl's connection-override flags from args (see
// parseKubeConfigFlags) so the probe targets the same cluster the kubectl
// child will contact.
//
// Resolve returns "" when the embedded kubectl is within ±1 minor of the
// server, when the probe fails for any reason (unreachable, missing or
// malformed kubeconfig, unparseable version), and for client-only
// invocations (config, completion, options, help, version --client),
// which skip the probe entirely.
//
// TODO(offline): when no cached kubectl matches and the network is down,
// fall back to the closest cached binary instead of failing the download.
// Gate it on a flag or RDD_KUBECTL_OFFLINE.
func Resolve(ctx context.Context, args []string) (string, error) {
	if skipResolver || os.Getenv(envSkipResolver) != "" {
		return "", nil
	}
	if isClientOnly(args) {
		return "", nil
	}
	embedded, err := embeddedVersion()
	if err != nil {
		// `go run ./cmd/rdd kubectl ...` and IDE debug builds skip the
		// Makefile's -ldflags -X, so componentbasever returns the
		// in-tree default `v0.0.0-master+$Format:%H$`, which fails
		// semver parsing. Fall through to the embedded kubectl rather
		// than break every dev invocation.
		logrus.WithError(err).Debug("kubectl resolver: embedded version not parseable; using embedded kubectl")
		return "", nil
	}
	server, ok := serverVersion(args)
	if !ok {
		return "", nil
	}
	if compatible(embedded, server) {
		return "", nil
	}
	return ensureCached(ctx, server)
}

// compatible reports whether a kubectl of client.Minor can drive a server
// of server.Minor under the standard Kubernetes skew of ±1 minor. A
// different Major rules out compatibility outright.
func compatible(client, server semver.Version) bool {
	if client.Major != server.Major {
		return false
	}
	diff := int64(client.Minor) - int64(server.Minor)
	return diff >= -1 && diff <= 1
}

// embeddedVersion reads the kubectl version baked into the rdd binary
// from k8s.io/component-base/version, populated by -X linker flags in
// the Makefile. Declared as a var so unit tests can swap it in to
// exercise the parse-failure fall-through that `go run` and IDE debug
// builds hit at runtime.
var embeddedVersion = func() (semver.Version, error) {
	return semver.ParseTolerant(componentbasever.Get().GitVersion)
}

// serverVersion contacts the cluster named by args (and the user's
// kubeconfig) and parses its reported version. ok is false on every
// failure path — missing kubeconfig, malformed config, unreachable
// cluster, unparseable version — because each is a legitimate reason
// to skip the resolver and fall through to the embedded kubectl. The
// three routine paths log at debug; the unparseable-version path logs
// at warn because an apiserver answering /version with garbage is the
// kind of surprise an operator wants to see at the default log level.
func serverVersion(args []string) (semver.Version, bool) {
	cfg, err := loadClientConfig(args)
	if err != nil {
		logrus.WithError(err).Debug("kubectl resolver: cannot load kubeconfig; using embedded kubectl")
		return semver.Version{}, false
	}
	// Force the fast probe timeout, overriding any --request-timeout the
	// args carried: the probe must stay snappy against an unreachable
	// cluster. The kubectl child still honors the user's --request-timeout.
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

// loadClientConfig builds a rest.Config from KUBECONFIG and the kubectl
// connection-override flags in args. Reusing kubectl's own flag binding
// (genericclioptions.ConfigFlags) keeps the probe aimed at the cluster
// the kubectl child will contact.
func loadClientConfig(args []string) (*rest.Config, error) {
	cf, err := parseKubeConfigFlags(args)
	if err != nil {
		return nil, err
	}
	return cf.ToRawKubeConfigLoader().ClientConfig()
}

// parseKubeConfigFlags parses kubectl's connection-override flags
// (--kubeconfig, --context, --server, --token, ...) out of args into a
// genericclioptions.ConfigFlags; TestParseKubeConfigFlags enumerates the
// bound surface. Unknown flags and positionals pass through silently, but a
// malformed known flag returns the parse error so the caller falls through
// to the embedded kubectl instead of probing the default cluster.
//
// The deprecated --username/--password flags stay unbound: binding them
// makes ToRawKubeConfigLoader prompt on stdin when the context lacks
// credentials, which a background probe must never do. The kubectl child
// still honors them.
func parseKubeConfigFlags(args []string) (*genericclioptions.ConfigFlags, error) {
	fs := pflag.NewFlagSet("rdd-kubectl", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	cf := genericclioptions.NewConfigFlags(true)
	cf.AddFlags(fs)
	return cf, fs.Parse(args)
}

// clientOnlySubcommands lists kubectl subcommands that never contact
// a cluster. config and completion manipulate local state only;
// kustomize renders local manifests; plugin lists local plugins on
// PATH; options prints kubectl's global flags; help prints help text.
var clientOnlySubcommands = map[string]bool{
	"completion": true,
	"config":     true,
	"kustomize":  true,
	"plugin":     true,
	"options":    true,
	"help":       true,
}

// isClientOnly reports whether args describe a kubectl invocation the
// resolver can skip: empty args, --help/-h, "version --client", and the
// clientOnlySubcommands. Everything else falls through to the probe. When
// in doubt it returns false (probe), since misclassifying a cluster-bound
// command as client-only would skip the skew check and run a mismatched
// embedded kubectl.
//
// It binds ConfigFlags plus the bare-bool globals (--help/-h, --client,
// --warnings-as-errors, --match-server-version). Klog flags (--v, ...)
// stay unbound so they don't leak into klog's process-global state;
// pflag's UnknownFlags handling treats `--v 4 config view` correctly,
// leaving [config, view] as positionals. A malformed bool like
// `--client=garbage` halts the parse with no positionals and lands in the
// empty-args branch (return true); kubectl rejects the same flag before
// contacting a cluster, so nothing mismatched runs.
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
		// `kubectl version` (no --client) probes the server for its
		// version. Treat that as cluster-bound so the resolver picks
		// a version-matched binary to do the probe.
		return client
	}
	return clientOnlySubcommands[subcommand]
}
