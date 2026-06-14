# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Helpers for the docker-command suite (tests/40-docker). Tests drive the engine
# through ctrctl() (commands.bash) so the suite can later run against nerdctl.

# Test image, pinned to a tag. TODO: pin by digest and mirror to ghcr.io to
# avoid Docker Hub pull limits, as the rancher-desktop suite does.
IMAGE_BUSYBOX=busybox:1.36

# Bring up the moby engine and point the docker CLI at it via DOCKER_HOST. Boots
# the VM on a run's first file and reuses it after (marker in BATS_RUN_TMPDIR).
start_docker_engine() {
    if ! load_var DOCKER_ENGINE_BOOTED; then
        rdd svc delete
        DOCKER_ENGINE_BOOTED=1
        save_var DOCKER_ENGINE_BOOTED
    fi
    # Creates and starts the instance on the first call; a fast no-op afterward.
    rdd set running=true
    if is_windows; then
        DOCKER_HOST="npipe:////./pipe/docker_engine"
    else
        run -0 rdd svc paths docker_socket
        DOCKER_HOST="unix://${output}"
    fi
    export DOCKER_HOST
}

# skip_unless_docker skips a docker-only command when the engine is containerd.
# nerdctl has no equivalent, so the gap is by design.
skip_unless_docker() { # [reason]
    using_docker || skip "${1:-not applicable to the containerd/nerdctl engine}"
}

# docker_has_compose reports whether the docker CLI has a working compose plugin.
docker_has_compose() {
    docker compose version >/dev/null 2>&1
}

# docker_has_build reports whether the docker CLI has the buildx plugin that
# docker build needs.
docker_has_build() {
    docker buildx version >/dev/null 2>&1
}
