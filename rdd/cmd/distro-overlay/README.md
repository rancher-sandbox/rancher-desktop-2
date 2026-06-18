# distro-overlay

`distro-overlay` merges Rancher Desktop assets into a pristine openSUSE distro at
build time. The distro stays "just the OS"; everything Rancher Desktop specific
lives in this repository and is layered on from a single manifest.

## Usage

    distro-overlay --manifest manifest.yaml --source ./files [--mtime T] <distro>

| Flag | Meaning |
|------|---------|
| `--manifest` | YAML manifest of entries to merge (required) |
| `--source` | Directory holding the file sources (default: the manifest's directory) |
| `--format` | `auto` (default), `raw`, or `tar`; `auto` detects by signature |
| `--output` | Output path for the tar format (default: overwrite the input) |
| `--mtime` | Timestamp for every entry: Unix epoch seconds or RFC3339 (default: now) |

`<distro>` is an uncompressed tarball or raw image. Decompress it first and
recompress afterward; the tool works on the uncompressed artifact.

## Distro forms

The same manifest drives both forms:

- **WSL tarball** — the tool appends each entry to the tar, dropping any base
  path the manifest overrides so the overlay wins.
- **Lima raw image** — the tool writes each entry into the ext4 root partition
  through go-diskfs, with no `resize2fs` and no root on the build host. The image
  must reserve free space at build time (kiwi `<size additive>` in the
  rancher-desktop-opensuse `config.kiwi`); an overlay that exceeds the reserve
  fails rather than growing the filesystem, so raise the reserve and rebuild.

## Manifest

See [`manifest.example.yaml`](manifest.example.yaml) for the schema and defaults:
files, directories, directory trees, and symlinks.

Every entry is stamped with one modification time from `--mtime` (default: the
tool's start time). Passing a deterministic value — for example a git commit
time — is what makes a rebuild reproducible.
