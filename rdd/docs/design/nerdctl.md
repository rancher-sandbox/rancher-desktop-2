# nerdctl Stub

The `rdd nerdctl` command runs nerdctl inside the Lima VM. nerdctl runs only
on Linux, so the binary lives in the guest; the stub rewrites path arguments
to where the guest mounts them, then invokes the guest binary with the
caller's stdio. The multicall dispatch in `cmd/rdd/multicall.go`
also accepts `nerdctl` as the invocation name, so a binlink can expose the
stub as plain `nerdctl`.

On macOS and Linux the guest mounts the host home directory at its original
path, so arguments pass through unchanged. On Windows the WSL2 guest
automounts drives under `/mnt`, so the stub rewrites `C:\src\app` to
`/mnt/c/src/app`. It first resolves a relative path against the host working
directory, and a drive-relative one such as `d:app` against the current
directory of drive D:, as Windows reports it through `GetFullPathName`.
Windows defines what `d:app` means, so the stub resolves it instead of
rejecting it as ambiguous. A rooted path without a drive, such as `\data` or
`/data`, takes the root of the working directory, its drive or its
`\\server\share`, as the "Rancher Desktop 1.x" stub did. The guest does not
mount UNC paths, so they pass through with forward slashes. A `--volume` or
`--mount` source that nerdctl reads as a volume name (`isNamedVolume` in
nerdctl's `pkg/mountutil`) passes through unchanged.

An argument that the stub rewrites always names a host path. With the working
directory on C:, `-v /mnt/c/src:/src` mounts `C:\mnt\c\src`, not the VM's
`/mnt/c/src`. To use a VM path in such an argument, run `rdd limavm shell rd
sudo nerdctl` with the nerdctl arguments instead. Flags that take a guest path,
such as `--device`, pass through unchanged.

## Runtime

`rdd nerdctl` disables cobra flag parsing and hands the raw arguments to
`pkg/nerdctlstub`, a port of the "Rancher Desktop 1.x" stub parser
(`src/go/nerdctl-stub/parse_args.go`). The parser must know, for every
nerdctl flag, whether it consumes a value (misjudging that shifts every
later argument) and whether to translate the value.

Three tables drive the parser:

| Table | Contents | Source |
| --- | --- | --- |
| command table | every command, its flags, whether each flag consumes a value | generated |
| overlay | the flags whose host paths the stub translates, with a handler kind for each | hand-curated |
| command handlers | positional-argument handlers (`container cp`, `compose cp`, `builder build`, `builder debug`) | hand-written |

The ten handler kinds, one per nerdctl path syntax, are input path, input
path that may also be `-` for stdin or a URI (`builder build --file`,
`--cosign-key`), comma-separated list of input paths (`run --env-file`),
output path, `--volume` (`host:container[:mode]`), `--mount` (CSV with
`source=`), builder cache (CSV with `src=`, `source=` or `dest=`), builder
output (builder-cache CSV, which nerdctl prefixes with `type=local,dest=`
when it has no `type=`), build context (`name=value`), and `--ssh`
(`id=path,...`).

`builder debug` runs `buildg`, which the guest does not ship, so the command
fails there; the stub translates its paths anyway.

The stub does not translate the root filesystem that `run --rootfs` and
`create --rootfs` take as their first positional argument, so name it by its
guest path, such as `/mnt/c/rootfs`. The same goes for a host path in the
value of a flag that the overlay leaves out, such as
`--security-opt seccomp=<path>` or `--log-opt log-path=<path>`. In
`container cp` and `compose cp`, a
container ID prefix or service name of one letter looks like a drive, so
start the host path with a drive and a backslash, as in `C:\src`, or with
`.`.

nerdctl splits a `--mount` value at every comma, and buildctl splits the
paths in an `--ssh` value the same way, so a path inside either cannot
contain a comma. The stub expands a relative path to
an absolute one, so a relative path in either fails the same way when the
working directory has a comma, such as `OneDrive - Acme, Inc`. In the builder
CSV values, the stub decodes CSV quotes as buildctl does, so a quoted path
there can contain a comma. A relative path there works in such a working
directory too, because the stub quotes the field that holds the expanded path.

The parse ends at an option that neither the command nor an enclosing command
lists, because the stub cannot tell whether it takes a value. From there the
stub passes the option and every later argument through unchanged, logs that
at debug level, and leaves nerdctl to accept or reject the option.

The stub never starts Rancher Desktop. It fails unless the App runs the
containerd engine; `rdd run nerdctl` starts the App first. After parsing, it
connects to the guest over SSH through `pkg/guestexec`, as `rdd limavm shell`
does, changes to the guest path of the host working directory, and runs
`sudo nerdctl` with the rewritten arguments. nerdctl needs root there. Run as
any other user, it switches to rootless mode even with an explicit `--address`.
Relative paths inside compose files and build contexts resolve in the guest
because the working directory maps to the same files; when the guest cannot see
the working directory, the command runs from the home directory instead.

The 1.x stub injects `--security-opt seccomp=/etc/rancher-desktop/seccomp.json`
into `run` and `create`; that profile is a 1.x distro asset which the
"Rancher Desktop 2" distro does not ship, so the port drops the injection.

## Command table generation

The command table must match the nerdctl version in the guest, the `nerdctl`
entry in `rdd/dependencies.yaml`; `TestTableMatchesGuestNerdctl` fails when
the two differ. The generator
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

The generate stage runs on Linux only: off-Linux builds of nerdctl compile out
`container cp` (the most path-sensitive command) and `apparmor`. Run
`make -C rdd generate-nerdctl`, which runs extract on the host and, off Linux,
wraps the generate stage in a Linux container. CI regenerates the table on
Linux and fails when the result differs from the checked-in copy.

The cobra tree has the true flag definitions, so lying help text costs nothing;
the 1.x generator parses `nerdctl <cmd> --help` and needs hand-patches for
flags like `compose down --volumes`, a boolean whose help renders a metavar.

## Overlay validation

nerdctl does not annotate which flags take paths (it never calls
`cobra.MarkFlagFilename`), so path classification is a human judgment. The
generator keeps that judgment honest:

- Every overlay entry must name an existing command and flag; a rename or
  removal upstream fails generation.
- Every value-consuming flag whose name or usage text suggests a path must
  appear in the overlay or in an acknowledged list of flags that the stub
  leaves alone; a new suspect fails generation with a message naming it.

A nerdctl bump therefore takes four steps. Raise `MAX_VERSION` in the
repository's top-level `scripts/dependencies/nerdctl.ts`, so that rddepman
moves `dependencies.yaml`.
Move `generate/go.mod` to the same version, along with the `tigron` pin that
its comment describes. Rerun the generator, and classify whatever it names.
