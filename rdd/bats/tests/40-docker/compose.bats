# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Compose commands against the rdd Docker engine: up, ps, logs, down. Compose is
# a docker CLI plugin; tests skip when it is absent.

local_setup_file() {
    start_container_engine
    if using_docker && docker_has_compose; then
        ctrctl pull --quiet "${IMAGE_BUSYBOX}"
    fi
}

local_setup() {
    if using_docker; then
        docker_has_compose || skip "docker compose plugin is not installed"
    fi
}

@test "compose up starts a project that ps lists" {
    cat >"${BATS_TEST_TMPDIR}/compose.yaml" <<EOF
services:
  app:
    image: ${IMAGE_BUSYBOX}
    command: sleep 300
EOF
    run -0 host_path "${BATS_TEST_TMPDIR}/compose.yaml"
    compose_file=${output}
    ctrctl compose --file "${compose_file}" --project-name rdd-compose up --detach
    run -0 ctrctl compose --file "${compose_file}" --project-name rdd-compose ps --format "{{.State}}"
    assert_output "running"
    ctrctl compose --file "${compose_file}" --project-name rdd-compose down
}

@test "compose logs returns service output" {
    cat >"${BATS_TEST_TMPDIR}/compose.yaml" <<EOF
services:
  app:
    image: ${IMAGE_BUSYBOX}
    command: echo composed
EOF
    run -0 host_path "${BATS_TEST_TMPDIR}/compose.yaml"
    compose_file=${output}
    # Run in the foreground so the service finishes before logs is read.
    # --abort-on-container-exit makes a foreground `up` return once the container exits.
    ctrctl compose --file "${compose_file}" --project-name rdd-compose-logs up --abort-on-container-exit
    run -0 ctrctl compose --file "${compose_file}" --project-name rdd-compose-logs logs
    assert_output --partial composed
    ctrctl compose --file "${compose_file}" --project-name rdd-compose-logs down
}
