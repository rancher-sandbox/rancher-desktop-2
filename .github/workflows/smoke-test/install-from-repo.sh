#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# This script is expected to run as root and install Rancher Desktop from the
# OBS project named in `OBS_PROJECT`.
#
# The installs skip recommended packages, so the smoke test fails when the
# Rancher Desktop package doesn't require a dependency that another package
# only recommends.
#
# Expected environment variables:
#   RD_VERSION
#      Rancher Desktop version; either major.minor (`1.20`) or the tag (`v1.20.0`).
#   OBS_PROJECT
#      The openSUSE Build Service project to use, in the form `isv:Rancher:dev`.
#   STABLE_VERSION
#      The package version that a stable OBS project builds from the release
#      tag (`2.0.0alpha.3` for `v2.0.0-alpha.3`).  The script accepts a package
#      with exactly this version as well as a devel build.

set -o errexit -o nounset

PACKAGE_NAME="rancher-desktop-2"

# shellcheck disable=2329 # The function is invoked dynamically
install_linux_debian() {
    local keyLocation=/usr/share version

    if [[ -d /etc/apt/keyrings ]]; then
        keyLocation=/etc/apt
    fi

    apt-get update
    apt-get install -y gnupg
    curl -s https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/deb/Release.key \
        | gpg --dearmor \
        > "${keyLocation}/keyrings/isv-rancher-dev-archive-keyring.gpg"
    echo "deb [signed-by=${keyLocation}/keyrings/isv-rancher-dev-archive-keyring.gpg] https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/deb/ ./"\
        > /etc/apt/sources.list.d/isv-rancher-dev.list
    apt-get update
    version=$(apt-cache show --quiet "${PACKAGE_NAME}" \
        | awk -F': ' "/^Version: (0\.release${RD_VERSION//./\\.}\.|${STABLE_VERSION//./\\.}-)/ { print \$2 }")
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        apt-cache show --quiet "${PACKAGE_NAME}" | sed 's@^@    @' >&2
        exit 1
    fi
    apt-get install -y --no-install-recommends "${PACKAGE_NAME}=${version}"
}

# shellcheck disable=2329 # The function is invoked dynamically
install_linux_opensuse() {
    zypper --non-interactive addrepo https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/rpm/${OBS_PROJECT}.repo
    zypper --non-interactive --gpg-auto-import-keys install libxml2-tools
    local version
    version=$(zypper --xmlout --non-interactive search --details --match-exact "${PACKAGE_NAME}" \
        | xmllint --xpath "string(//solvable[@kind='package']/@edition[contains(., '0.release${RD_VERSION}.') or starts-with(., '${STABLE_VERSION}-')])" -)
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        zypper --non-interactive search --details --match-exact "${PACKAGE_NAME}" | sed 's@^@    @' >&2
        exit 1
    fi
    zypper --non-interactive install --no-recommends "${PACKAGE_NAME}=${version}"
}

# shellcheck disable=2329 # The function is invoked dynamically
install_linux_fedora() {
    dnf config-manager addrepo --from-repofile=https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/fedora/${OBS_PROJECT}.repo
    local version
    version=$(dnf --quiet info --showduplicates "${PACKAGE_NAME}.$(uname -m)" \
        | awk -F: "\$1 ~ /Version/ && \$2 ~ /^ *(0\.release${RD_VERSION//./\\.}|${STABLE_VERSION//./\\.} *\$)/ { print \$2 }" \
        | tr -d '[:space:]')
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        dnf --quiet info --showduplicates "${PACKAGE_NAME}.$(uname -m)" | sed 's@^@    @' >&2
        exit 1
    fi
    dnf --assumeyes --setopt=install_weak_deps=False install "${PACKAGE_NAME}-${version}"
}

# Chromium only uses its setuid sandbox when the helper is setuid root, so a
# packaging mistake here degrades the sandbox silently.
verify_sandbox_helper() {
    local sandbox="/opt/${PACKAGE_NAME}/chrome-sandbox" mode
    mode=$(stat --format='%a %U' "$sandbox")
    if [[ "$mode" != "4755 root" ]]; then
        printf "%s is '%s', expected '4755 root'\n" "$sandbox" "$mode" >&2
        exit 1
    fi
}

main() {
    RD_VERSION=$(grep --only-matching '\([0-9]\+\.[0-9]\+\)' <<< "$RD_VERSION")
    : "${STABLE_VERSION:?}"
    source /etc/os-release
    for id in ${ID:-} ${ID_LIKE:-}; do
        if [[ "$(type -t "install_linux_$id")" == function ]]; then
            eval "install_linux_$id"
            verify_sandbox_helper
            exit 0
        fi
    done
    printf "Could not find supported distribution in %s\n" "${ID:-} ${ID_LIKE:-}" >&2
    exit 1
}

main
