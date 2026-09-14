#!/usr/bin/env bash

# This script is expected to run as root and install Rancher Desktop from the
# repository obs://isv:Rancher:dev
# Expected environment variables:
#   RD_VERSION
#      Rancher Desktop version; either major.minor (`1.20`) or the tag (`v1.20.0`).
#   OBS_PROJECT
#      The openSUSE Build Service project to use, in the form `isv:Rancher:dev`.
#   RD_COMMIT
#      Full commit sha of the release under test, used to reject a stale
#      package. The check is skipped when this is empty.

set -o errexit -o nounset

PACKAGE_NAME="rancher-desktop-2"

# OBS publishes only the newest successful build of a package, so a failed
# rebuild leaves the previous one in the repository and every version matcher
# still selects it. The version ends in the commit OBS built from, which is the
# only way to tell that apart from the release.
# shellcheck disable=2329 # Called from the dynamically invoked install functions
verify_package_commit() {
    local version=$1 built_from
    if [[ -z "${RD_COMMIT:-}" ]]; then
        return
    fi
    built_from=${version%%-*}    # drop the packaging release suffix
    built_from=${built_from##*.} # the commit is the last version component
    if [[ ! "$built_from" =~ ^[0-9a-f]{7,}$ ]]; then
        printf "Found no commit at the end of version '%s'\n" "$version" >&2
        exit 1
    fi
    if [[ "$RD_COMMIT" != "$built_from"* ]]; then
        printf "%s %s was built from %s, but the release is %s\n" \
            "$PACKAGE_NAME" "$version" "$built_from" "$RD_COMMIT" >&2
        printf "The repository is serving an older build than the release.\n" >&2
        exit 1
    fi
}

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
        | awk -F': ' "/^Version: 0\.release${RD_VERSION//./\\.}\./ { print \$2 }")
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        apt-cache show --quiet "${PACKAGE_NAME}" | sed 's@^@    @' >&2
        exit 1
    fi
    verify_package_commit "${version}"
    apt-get install -y "${PACKAGE_NAME}=${version}"
}

# shellcheck disable=2329 # The function is invoked dynamically
install_linux_opensuse() {
    zypper --non-interactive addrepo https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/rpm/${OBS_PROJECT}.repo
    zypper --non-interactive --gpg-auto-import-keys install libxml2-tools
    local version
    version=$(zypper --xmlout --non-interactive search --details --match-exact "${PACKAGE_NAME}" \
        | xmllint --xpath "string(//solvable[@kind='package']/@edition[contains(., '0.release${RD_VERSION}.')])" -)
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        zypper --non-interactive search --details --match-exact "${PACKAGE_NAME}" | sed 's@^@    @' >&2
        exit 1
    fi
    verify_package_commit "${version}"
    zypper --non-interactive install "${PACKAGE_NAME}=${version}"
}

# shellcheck disable=2329 # The function is invoked dynamically
install_linux_fedora() {
    dnf config-manager addrepo --from-repofile=https://download.opensuse.org/repositories/${OBS_PROJECT//:/:/}/fedora/${OBS_PROJECT}.repo
    local version
    version=$(dnf --quiet info --showduplicates "${PACKAGE_NAME}.$(uname -m)" \
        | awk -F: "\$1 ~ /Version/ && \$2 ~ /0\.release${RD_VERSION//./\\.}/ { print \$2 }" \
        | tr -d '[:space:]')
    if [[ -z "${version}" ]]; then
        echo "Could not find any versions of ${PACKAGE_NAME}" >&2
        dnf --quiet info --showduplicates "${PACKAGE_NAME}.$(uname -m)" | sed 's@^@    @' >&2
        exit 1
    fi
    verify_package_commit "${version}"
    dnf --assumeyes install "${PACKAGE_NAME}-${version}"
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
