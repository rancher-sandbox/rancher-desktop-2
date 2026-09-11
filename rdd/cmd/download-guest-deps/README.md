# download-guest-deps

`download-guest-deps` stages the guest dependencies for the rdd build to embed.
It currently stages only the distro image, as the raw disk image that Lima's
`vz` and `qemu` drivers boot or, for a Windows build, as the rootfs tarball
that WSL2 imports.

It reads `dependencies.yaml`, which `yarn rddepman guest` writes, picks the
asset for the build target, and checks the downloaded bytes against the sha256
checksum recorded there. It keeps downloads in a cache outside the source tree,
so every checkout and worktree shares one copy, and it skips a staged file that
already matches its checksum.

Run it from `rdd/` before `go build`; the default paths are relative to that
directory.

## Usage

```sh
go run ./cmd/download-guest-deps [--manifest FILE] [--dest DIR] [--cache DIR] [--os OS] [--arch ARCH]
```

| Option | Default | Meaning |
|---|---|---|
| `--manifest` | `dependencies.yaml` | The guest dependency manifest to read. |
| `--dest` | `pkg/embedded` | The directory to stage the assets into. |
| `--cache` | see [Cache](#cache) | The directory that keeps verified downloads. |
| `--os` | the host's | The operating system the build targets, as a `GOOS` value. |
| `--arch` | the host's | The architecture the build targets, as a `GOARCH` value. |

A cross-compiling build passes `--os` and `--arch`. Setting `GOOS` or `GOARCH`
in the environment instead would cross-compile this command as well, and leave
nothing that runs on the build machine.

A Windows target gets `distro.tar.xz` and any other target gets
`distro.raw.xz`. Either one is the Linux asset for `--arch`, because the guest
VM runs Linux.

The command exits 0 once everything is staged, 1 on any failure, and 2 on a
usage error. Failing to prune the cache only prints a warning.

## Cache

The default cache is `rancher-desktop.guest-deps` in the user cache directory,
which is `~/Library/Caches` on macOS, `$XDG_CACHE_HOME` or `~/.cache` on Linux,
and `%LocalAppData%` on Windows. Each download is kept at
`<name>/v<version>/<file>`, for example
`distro/v0.2.7/distro.v0.2.7.arm64.raw.xz`, so a new version is fetched beside
the old one.

The command checks a cached file against the manifest before copying it, and
downloads it again if it does not match. Each run then removes the downloads no
run has used for seven days. The prune touches nothing outside those version
directories.

## Network

The command drops a transfer that goes 30 seconds without receiving data and
tries again. An asset gets four attempts, all within two hours. Between
attempts it waits for the delay the server names, or backs off from two
seconds when it names none, but never longer than 30 seconds. A checksum
mismatch fails the run at once, and so does a client error such as a 404. A
408, a 429, or any response that names a delay is retried instead. A long
download logs its progress every five seconds.

## Trying it out

These steps stage into a scratch directory with its own cache, so they leave
`pkg/embedded` and your real cache alone and the first run has to download.
Run them from `rdd/` in a POSIX shell.

```sh
scratch=$(mktemp -d)
go run ./cmd/download-guest-deps --dest "$scratch/embedded" --cache "$scratch/cache"
```

The first run downloads the raw image for this machine's architecture, 200 to
250 MiB, and stages it as `$scratch/embedded/distro.raw.xz`. Running the same
command again reports the staged file up to date and does no network work.

```sh
go run ./cmd/download-guest-deps --dest "$scratch/embedded" --cache "$scratch/cache"
```

Damage the staged file, and the next run copies it back from the cache, still
without downloading.

```sh
printf broken > "$scratch/embedded/distro.raw.xz"
go run ./cmd/download-guest-deps --dest "$scratch/embedded" --cache "$scratch/cache"
```

Staging for Windows downloads the rootfs tarball, about 180 MiB, and stages it
as `$scratch/embedded/distro.tar.xz`.

```sh
go run ./cmd/download-guest-deps --dest "$scratch/embedded" --cache "$scratch/cache" --os windows --arch amd64
```

Remove the scratch directory when you are done.

```sh
rm -rf "$scratch"
```
