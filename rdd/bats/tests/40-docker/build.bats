# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Build commands against the rdd Docker engine. docker build needs the host
# buildx plugin; tests skip when it is absent.

local_setup_file() {
    start_docker_engine
    if docker_has_build; then
        ctrctl pull --quiet "${IMAGE_BUSYBOX}"
    fi
}

local_setup() {
    docker_has_build || skip "docker buildx plugin is not installed"
}

@test "build creates an image from a Dockerfile" {
    cat >"${BATS_TEST_TMPDIR}/Dockerfile" <<EOF
FROM ${IMAGE_BUSYBOX}
RUN echo built >/built.txt
EOF
    run -0 host_path "${BATS_TEST_TMPDIR}"
    ctrctl build --tag rdd-build:v1 "${output}"
    run -0 ctrctl run --rm rdd-build:v1 cat /built.txt
    assert_output built
    ctrctl rmi rdd-build:v1
}

@test "build --build-arg passes a value into the build" {
    cat >"${BATS_TEST_TMPDIR}/Dockerfile" <<EOF
FROM ${IMAGE_BUSYBOX}
ARG GREETING
RUN echo "\${GREETING}" >/greeting.txt
EOF
    run -0 host_path "${BATS_TEST_TMPDIR}"
    ctrctl build --build-arg GREETING=hi --tag rdd-build-arg:v1 "${output}"
    run -0 ctrctl run --rm rdd-build-arg:v1 cat /greeting.txt
    assert_output hi
    ctrctl rmi rdd-build-arg:v1
}
