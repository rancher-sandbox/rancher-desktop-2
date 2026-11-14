#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

set -o errexit -o nounset -o pipefail

# Render the QEMU and firmware Requires of the Linux packages that OBS
# builds from the RPM spec, for each distro and architecture, and compare
# them with the expected list below. Needs rpmspec, perl and git.
# Usage: scripts/check-linux-package-deps.sh [SPEC]

cd "$(dirname "$0")/.."
spec=$(realpath "${1:-packaging/linux/rancher-desktop.spec}")
pattern='qemu|ovmf|efi|edk2'

# The x86_64 RPM rows don't list a firmware package, because the distro's
# qemu pulls it in. Fedora's qemu-system-x86-core requires edk2-ovmf, and
# openSUSE's qemu-x86 requires qemu-ovmf-x86_64.
expected=$(
    cat <<'EOF'
debian amd64: ovmf qemu-system-x86 qemu-utils
debian arm64: qemu-efi-aarch64 qemu-system-arm qemu-utils
fedora aarch64: edk2-aarch64 qemu
fedora x86_64: qemu
opensuse aarch64: qemu qemu-img qemu-uefi-aarch64
opensuse x86_64: qemu qemu-img
EOF
)

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Render the Debian rows with debbuild (tag 24.12.0), which OBS uses to
# build the debs from the same spec.
src=${work}/src
git init --quiet "${src}"
git -C "${src}" fetch --quiet --depth=1 https://github.com/debbuild/debbuild.git \
    65c140bf902aa4860709a899a0f197fd7aa05e56
git -C "${src}" checkout --quiet FETCH_HEAD

conf=${work}/conf
mkdir -p "${conf}/macros.d" "${work}/bin" "${work}/lib/Locale"
# Install the macros the way debbuild's Makefile does.
cp "${src}/macros/macros.in" "${conf}/macros"
cp "${src}/macros/platform.in" "${conf}/macros.d/macros.00platform"

# Stand-in dpkg-architecture and lsb_release commands describe a Debian
# host of the architecture in PROBE_ARCH. debbuild calls lsb_release only
# on a host without /etc/os-release, such as macOS.
cat >"${work}/bin/dpkg-architecture" <<'EOF'
#!/bin/sh
case "$1" in
  -qDEB_HOST_ARCH|-qDEB_BUILD_ARCH) echo "$PROBE_ARCH" ;;
  -qDEB_HOST_ARCH_OS) echo linux ;;
  -qDEB_HOST_GNU_CPU) echo "$PROBE_CPU" ;;
  -qDEB_HOST_MULTIARCH) echo "$PROBE_CPU-linux-gnu" ;;
esac
EOF
printf '#!/bin/sh\nprintf "Debian\\n11\\nbullseye\\n"\n' >"${work}/bin/lsb_release"
chmod +x "${work}/bin/dpkg-architecture" "${work}/bin/lsb_release"
# A stand-in for Locale::gettext, which macOS's system perl lacks.
cat >"${work}/lib/Locale/gettext.pm" <<'EOF'
package Locale::gettext;
use Exporter 'import';
our @EXPORT = qw(gettext bindtextdomain textdomain);
sub gettext { $_[0] } sub bindtextdomain {} sub textdomain {}
1;
EOF
# debbuild names its distro macro after the ID in /etc/os-release, so on
# an Ubuntu runner it defines %ubuntu, while the spec tests %{?debian}.
# Define %debian to render the spec as a Debian build would.
printf '%%__dpkg_architecture %s\n%%__lsb_release %s\n%%debian 11\n' \
    "${work}/bin/dpkg-architecture" "${work}/bin/lsb_release" \
    >"${conf}/macros.d/macros.zz-check"

# Point debbuild at that config the way debbuild's Makefile does, with a
# `debconfigdir:` line in its __DATA__ section. An added END block prints
# the main package's Requires, one per line, when debbuild exits after
# parsing the spec.
awk -v end='END { print "$_\n" for @{$pkgdata{main}{requires}} }' \
    -v conf="${conf}" '
  /^__DATA__$/ { print end; print; print "debconfigdir:" conf; next }
  { print }' "${src}/debbuild" >"${work}/debbuild"

# Print a row's name and its matching package names. debbuild prints
# Requires in spec order and rpmspec prints them sorted, so sort both
# the same way.
row() {
    local name=$1
    shift
    if ! "$@" >"${work}/out" 2>"${work}/err" ||
        ! grep --extended-regexp --quiet "${pattern}" "${work}/out"; then
        echo "Rendering ${name} failed or did not find any QEMU packages:" >&2
        cat "${work}/err" >&2
        exit 1
    fi
    # Keep each dependency's package name, dropping any version constraint.
    local packages
    packages=$(awk '{ print $1 }' "${work}/out" | grep --extended-regexp "${pattern}" | LC_ALL=C sort | paste -s -d ' ' -)
    echo "${name}: ${packages}"
}

debian() {
    PROBE_ARCH=$1 PROBE_CPU=$2 perl -I"${work}/lib" "${work}/debbuild" \
        --nodeps --nobuild -bb --define "_topdir ${work}/top" "${spec}"
}

actual=$(
    row "debian amd64" debian amd64 x86_64
    row "debian arm64" debian arm64 aarch64
    for arch in aarch64 x86_64; do
        row "fedora ${arch}" rpmspec --query --requires --target="${arch}" --define 'fedora 40' "${spec}"
    done
    for arch in aarch64 x86_64; do
        row "opensuse ${arch}" rpmspec --query --requires --target="${arch}" "${spec}"
    done
)

if ! diff --unified --label expected --label actual <(echo "${expected}") <(echo "${actual}"); then
    echo "Linux package dependencies changed. Fix the spec, or update the expected list in $0 if the change is intended." >&2
    exit 1
fi
