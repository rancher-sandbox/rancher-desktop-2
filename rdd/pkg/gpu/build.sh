#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Builds librocdxg.so from the ROCm/librocdxg sources. This runs inside a
# throwaway container; see gpu.go for how the inputs are staged. Inputs are
# mounted read-only at /in and the result is written to /out.

set -o errexit -o nounset -o pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends cmake g++ make ca-certificates

work=$(mktemp -d)
tar -xzf /in/librocdxg.tar.gz -C "$work" --strip-components=1

# The library dlopens libhsa-runtime64 and relies on the RUNPATH of
# /opt/rocm/lib that upstream sets at install time. Take the installed copy
# rather than the one in the build tree, whose RUNPATH has an extra empty entry.
# With the pinned inputs this is byte-identical to AMD's rocdxg-roct deb.
cmake -S "$work" -B "$work/build" \
    -DCMAKE_BUILD_TYPE=Release \
    -DWIN_SDK=/in/winsdk
cmake --build "$work/build" -j"$(nproc)"
cmake --install "$work/build" --prefix "$work/install" --component binary

cp -L "$work/install/lib/librocdxg.so" /out/librocdxg.so.tmp
mv /out/librocdxg.so.tmp /out/librocdxg.so
