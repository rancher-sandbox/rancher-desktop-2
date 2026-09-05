# nerdctl Stub

The `rdd nerdctl` command runs nerdctl inside the Lima VM. nerdctl runs only
on Linux, so the binary lives in the guest; the stub rewrites path arguments
to where the guest mounts them, then invokes the guest binary with the
caller's stdio. The multicall dispatch in `cmd/rdd/multicall.go`
also accepts `nerdctl` as the invocation name, so a binlink can expose the
stub as plain `nerdctl`.

On macOS and Linux the guest mounts the host home directory at its original
path, so arguments pass through unchanged. On Windows the WSL2 guest
automounts drives under `/mnt`, so the stub rewrites `C:\Users\jan\app` to
`/mnt/c/Users/jan/app`.

## Runtime

`rdd nerdctl` disables cobra flag parsing and hands the raw arguments to
`pkg/nerdctlstub`, a port of the "Rancher Desktop 1.x" stub parser
(`src/go/nerdctl-stub/parse_args.go`). The parser must know, for every
nerdctl flag, whether it consumes a value — misjudging that shifts every
later argument — and which values contain host paths.

Three tables drive the parser:

| Table | Contents | Source |
| --- | --- | --- |
| command table | every command, its flags, whether each flag consumes a value | generated |
| overlay | the flags whose values contain host paths, with a handler kind for each | hand-curated |
| command handlers | positional-argument handlers (`container cp`, `builder build`, `image import`) | hand-written |

Six handler kinds cover nerdctl's path syntaxes: input path, output path,
`--volume` (`host:container[:rw]`), `--mount` (CSV with `source=`), builder
cache (CSV with `src=`/`dest=`), and build context (`key=value` CSV).

After parsing, the stub connects to the guest over SSH (the same plumbing as
`rdd limavm shell`), changes to the translated working directory, and execs
nerdctl with the rewritten arguments. Relative paths inside compose files and
build contexts resolve in the guest because the working directory maps to the
same files. Cleanup handlers run after the process exits.

The 1.x stub injects `--security-opt seccomp=/etc/rancher-desktop/seccomp.json`
into `run` and `create`; that profile is a 1.x distro asset which the
"Rancher Desktop 2" distro does not ship, so the port drops the injection.

## Command table generation

The command table must match the nerdctl version in the guest —
`NERDCTL_VERSION` in the distro's `root/build/versions.env`. The generator
(`pkg/nerdctlstub/generate`, a separate Go module) pins the same version of
`github.com/containerd/nerdctl/v2` and derives the table from the real cobra
command tree in two stages:

1. **extract** parses `cmd/nerdctl/main.go` and `main_linux.go` from the
   pinned module with `go/ast`, collects the constructor calls passed to
   `AddCommand`, and writes them to `zz_generated_commands.go` inside the
   generator. For stage 2, it also collects the flag names registered in
   `initRootCmdFlags`.
2. **generate** rebuilds the root command — the one part of nerdctl that is
   not importable (`package main`) — from a small replica of
   `initRootCmdFlags`, adds the extracted subcommand constructors, walks the
   resulting tree through the cobra API, and writes
   `pkg/nerdctlstub/nerdctl_commands_generated.go`. It checks the replica
   against the extracted flag names, so a root-flag change upstream fails
   generation instead of silently drifting.

The generator runs on Linux only: off-Linux builds of nerdctl compile out
`container cp` (the most path-sensitive command) and `apparmor`. Run
`make -C rdd generate-nerdctl` — on macOS it wraps the two stages in a Linux
container. CI regenerates the table on Linux and fails when the result
differs from the checked-in copy.

Reflection sees the true flag definitions, so lying help text costs nothing;
the 1.x generator parses `nerdctl <cmd> --help` and needs hand-patches for
flags like `compose down --volumes`, a boolean whose help renders a metavar.

## Overlay validation

nerdctl does not annotate which flags take paths (it never calls
`cobra.MarkFlagFilename`), so path classification is a human judgment. The
generator keeps that judgment honest:

- Every overlay entry must name an existing command and flag; a rename or
  removal upstream fails generation.
- Every value-consuming flag whose name or usage text suggests a path must
  appear in the overlay or in an acknowledged not-a-path list; a new suspect
  fails generation with a message naming it.

A nerdctl bump therefore reduces to: bump the version in `generate/go.mod`
(alongside `versions.env`), rerun the generator, and classify whatever it
names.
