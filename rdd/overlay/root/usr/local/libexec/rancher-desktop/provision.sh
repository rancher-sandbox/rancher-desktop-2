#!/usr/bin/bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# rancher-desktop-provision.service runs this script on every boot, with the
# settings from /etc/sysconfig/rancher-desktop in its environment.
# shellcheck disable=SC2154 # The unit's EnvironmentFile= sets these variables.

set -o errexit -o nounset -o pipefail -o xtrace

# Restart rancher-desktop.target on exit, even if a step below fails. The
# container engine belongs to this target but does not depend on
# Kubernetes, so a failed k3s install must not leave it stopped. --no-block
# returns immediately; the kubeconfig probe in rdd's Lima template waits for
# k3s to start.
trap 'systemctl start --no-block rancher-desktop.target' EXIT

systemctl stop rancher-desktop.target

# Enable the requested container engine and disable the other.
# containerd.service has a Conflicts=docker, so we must not start both.
CONTAINERD_ACTION=$([[ ${CONTAINER_ENGINE} == containerd ]] && echo enable || echo disable)
DOCKER_ACTION=$([[ ${CONTAINER_ENGINE} == moby ]] && echo enable || echo disable)
systemctl "${CONTAINERD_ACTION}" containerd.service buildkitd.service
systemctl "${DOCKER_ACTION}" docker.service docker.socket

if [[ ${KUBERNETES_ENABLED} == "true" ]]; then
    export INSTALL_K3S_VERSION="v${KUBERNETES_VERSION}+k3s1"
    INSTALLED_VERSION=""
    # awk must read to EOF; `{print; exit}` closes the pipe while k3s
    # is still writing, and the SIGPIPE propagates via pipefail + errexit.
    [[ -x /usr/local/bin/k3s ]] && INSTALLED_VERSION=$(/usr/local/bin/k3s --version | awk 'NR==1{print $3}')
    if [[ ${INSTALL_K3S_VERSION} != "${INSTALLED_VERSION}" ]]; then
        # Don't enable k3s at install time; add it as a want of rancher-desktop.target instead.
        export INSTALL_K3S_SKIP_ENABLE=true
        # Before reinstalling, remove any existing k3s want; otherwise a
        # failed upgrade leaves rancher-desktop.target still wanting the
        # previous k3s version. daemon-reload rebuilds systemd's in-memory
        # dependency tree from disk so the EXIT trap's restart honors the
        # removal even on the failure path, where add-wants below (the only
        # other reload) never runs. The want is re-added on success below.
        rm -f /etc/systemd/system/rancher-desktop.target.wants/k3s.service
        systemctl daemon-reload
        (
            export PATH=/usr/local/sbin:/usr/local/bin:/usr/bin:/sbin:/bin
            # The interface can have an address before the host-switch
            # network is routable, so the install can fail right after a
            # restart. Retry the whole download-and-install until it
            # succeeds; the curl timeouts bound the installer-script fetch
            # so a black-holed network fails fast instead of hanging that
            # step. --show-error surfaces the error without --silent's
            # progress meter spamming the log.
            attempt=0
            until curl --fail --silent --show-error --location --connect-timeout 10 --max-time 60 https://get.k3s.io | sh -; do
                attempt=$((attempt + 1))
                if ((attempt >= 30)); then
                    echo "k3s install failed after ${attempt} attempts" >&2
                    # Fail the unit so `systemctl status` shows the error; the
                    # EXIT trap still restarts the engine, which does not need
                    # k3s.
                    exit 1
                fi
                sleep 2
            done
        )
        systemctl add-wants rancher-desktop.target k3s.service
    fi

    if [[ ${CONTAINER_ENGINE} == "containerd" ]]; then
        echo "K3S_CONTAINER_ENGINE_OPTIONS=--container-runtime-endpoint /run/k3s/containerd/containerd.sock" \
            >/etc/systemd/system/k3s.service.env
    else
        echo "K3S_CONTAINER_ENGINE_OPTIONS=--docker" >/etc/systemd/system/k3s.service.env
    fi

    # Remove stale kubeconfig so k3s regenerates it on start.
    rm -f /etc/rancher/k3s/k3s.yaml
fi

# rancher-desktop.target is restarted by the EXIT trap above.
