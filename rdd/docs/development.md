# Rancher Desktop Daemon Development

For design details, please refer to [design/](design/).

## Prerequisites

This section only applies to building (and testing) Rancher Desktop Daemon; for
prerequisites for running it, please refer to user documentation.

We of course require all the things running RDD does (e.g. the ability to run
VMs).

On all platforms, we expect at least:
- Git
- GNU Make 4 or higher. (macOS ships with 3.81, which is too old.)
- GNU coreutils, and other basic tools like `gawk`, `gzip`, and `sed`.
- GNU bash version 5 or higher. (macOS ships with 3.2, which is too old.)
- Golang compiler as listed in `go.mod` (from go.dev, not the GCC toolchain or
  others).
- jq
- Perl (for check-spelling only).

We currently only support building from a checked-out source tree (i.e. with the
`.git` directory available, including any tags).

For all platforms, we only support whichever OS version we are using, which is
generally the latest release versions.

### macOS

On macOS, we expect Xcode command line tools to be available.

### Windows

Development is supported under WSL2 and MSYS2.  Development using `cmd.exe`,
PowerShell, Git Bash, or Cygwin is not supported.

All RDD processes run as Win32 executables.  Developing for Linux on a Windows
host is only supported when using a full Linux VM, whether or not WSL
integration is enabled.

#### WSL2

WSL interop must be enabled (we test for `winver.exe` being around).

#### MSYS2

Install MSYS2, Git, and Go natively on Windows (e.g. via scoop) rather than
through pacman; the MSYS2 versions may behave differently from what CI uses.

```
scoop install msys2 git go
```

Then install build dependencies inside MSYS2:

```bash
pacman --sync --needed jq make mingw-w64-x86_64-gcc openbsd-netcat openssh
```

The BATS test harness exports `MSYS_NO_PATHCONV=1` to prevent MSYS2 from
converting URL-like arguments (e.g. `/passthrough/demo/hello`) into Windows
paths.  The `rdd()` wrapper in `bats/helpers/commands.bash` handles explicit
path conversion for arguments that need it.

## Building against a Lima fork

rdd links Lima from the `github.com/lima-vm/lima/v2` module and builds its
embedded guest agent from the same module, so a fork changes both the host and
the guest. When rdd needs Lima changes that upstream has not merged yet, point
the module at a fork in `go.mod`:

```bash
go mod edit -replace=github.com/lima-vm/lima/v2=github.com/rancher-sandbox/lima/v2@<commit>
go mod tidy
```

`go mod tidy` turns the commit into a pseudo-version. To check which source
directory the build uses, run `go list -m -f '{{.Dir}}' github.com/lima-vm/lima/v2`.

Above the `replace`, add a comment that lists the upstream pull requests that
the fork waits for. `go mod tidy` keeps the comment. Once they have all
merged, remove the `replace` and update the `require` line to an upstream
version that contains them. The `require` line still names the version from
before the fork, so removing only the `replace` would build that version,
without the fork's changes.

The WSL guest agent in `src/wsl-guestagent` has its own `go.mod`, and `make`
builds it as a separate main module. Go applies only the main module's
`replace` directives, so the `replace` in rdd's `go.mod` does not affect the WSL
guest agent.

A fork gets upstream fixes, security fixes included, only when someone rebases
it, so rebase the fork onto upstream whenever you update it. Between updates,
nothing tells you that upstream has fixed a vulnerability. Dependabot does not
alert on Lima while the `replace` points it at a fork, because GitHub's
dependency graph leaves the replaced module out. So as long as the `replace` is
in place, watch [Lima's releases](https://github.com/lima-vm/lima/releases) and
rebase the fork when a release fixes a vulnerability. The release notes can
name a security fix weeks before the advisory is published.

To tell whether upstream already has a fork commit, compare the code. Lima
squashes pull requests and backports fixes to release branches as new commits,
so `git cherry` and `git merge-base --is-ancestor` report such commits as
missing even after they have merged.

## Debugging with Delve

`dlv debug` names the binary that it builds `__debug_bin` or
`__debug_bin<random>`, and rdd rejects both. So give the binary an
[accepted name](design/cmd_other.md#rdd-kubectl) with `--output`. rdd turns
on [developer mode](design/environment.md) by itself only when its binary is
in a directory beside `cmd/`, so set `RDD_DEVELOPER_MODE=1` for the debug
build, as in
`RDD_DEVELOPER_MODE=1 dlv debug --output /tmp/rdd ./cmd/rdd -- svc status`.
Set the output path and the variable in your editor's launch configuration
too.
