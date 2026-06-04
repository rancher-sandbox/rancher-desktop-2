# fake-kube

Test fixtures for `7-kubectl-cache.bats`, sitting next to the test that uses them.

## Programs

- **`kubectl/`** — a stand-in `kubectl` binary. Prints `fake-kubectl: <args>`
  and exits 0. The BATS test serves it from the fake mirror and asserts
  the output to confirm the resolver downloaded, sha-verified, and exec'd
  it. Compiles to a real PE on Windows so `CreateProcess` accepts it.

- **`server/`** — a single HTTP server playing two roles:
  1. The Kubernetes apiserver: `GET /version` returns a configurable
     `version.Info` JSON. The resolver hits this first to decide whether
     to download.
  2. The Kubernetes release mirror: `GET /release/...` serves files from
     a configurable directory tree. `RDD_KUBECTL_MIRROR` points at this
     URL during the test.

## Why a separate Go module

Keeps this directory off the main `./...` build path so the fixtures
never enter the rdd binary, and so the parent `go.mod` stays free of
test-only dependencies.

## Build

`local_setup_file()` in `7-kubectl-cache.bats` builds both binaries
into `$BATS_FILE_TMPDIR` on each test run.
