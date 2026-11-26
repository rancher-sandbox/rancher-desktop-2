# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Build commands against the rdd container engine. Under moby, docker build
# needs the host buildx plugin; tests skip when it is absent.

local_setup_file() {
    start_container_engine
    if ! using_docker || docker_has_build; then
        ctrctl pull --quiet "${IMAGE_BUSYBOX}"
    fi
}

local_setup() {
    if using_docker; then
        docker_has_build || skip "docker buildx plugin is not installed"
    fi
}

@test "build creates an image from a Dockerfile" {
    create_host_tmpdir
    cat >"${HOST_TMPDIR}/Dockerfile" <<EOF
FROM ${IMAGE_BUSYBOX}
RUN echo built >/built.txt
EOF
    run -0 host_path "${HOST_TMPDIR}"
    ctrctl build --tag rdd-build:v1 "${output}"
    # nerdctl run --rm can log a signal-forwarding error on stderr.
    run_e -0 ctrctl run --rm rdd-build:v1 cat /built.txt
    assert_output built
    ctrctl rmi rdd-build:v1
}

@test "build --build-arg passes a value into the build" {
    create_host_tmpdir
    cat >"${HOST_TMPDIR}/Dockerfile" <<EOF
FROM ${IMAGE_BUSYBOX}
ARG GREETING
RUN echo "\${GREETING}" >/greeting.txt
EOF
    run -0 host_path "${HOST_TMPDIR}"
    ctrctl build --build-arg GREETING=hi --tag rdd-build-arg:v1 "${output}"
    run_e -0 ctrctl run --rm rdd-build-arg:v1 cat /greeting.txt
    assert_output hi
    ctrctl rmi rdd-build-arg:v1
}
