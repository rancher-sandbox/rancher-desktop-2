# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Compose commands against the rdd container engine: up, ps, logs, down. Under
# moby, compose is a docker CLI plugin; tests skip when it is absent.

local_setup_file() {
    start_container_engine
    if ! using_docker || docker_has_compose; then
        ctrctl pull --quiet "${IMAGE_BUSYBOX}"
    fi
}

local_setup() {
    if using_docker; then
        docker_has_compose || skip "docker compose plugin is not installed"
    fi
}

@test "compose up starts a project that ps lists" {
    create_host_tmpdir
    cat >"${HOST_TMPDIR}/compose.yaml" <<EOF
services:
  app:
    image: ${IMAGE_BUSYBOX}
    command: sleep 300
EOF
    run -0 host_path "${HOST_TMPDIR}/compose.yaml"
    compose_file=${output}
    ctrctl compose --file "${compose_file}" --project-name rdd-compose up --detach
    if using_docker; then
        run -0 docker compose --file "${compose_file}" --project-name rdd-compose ps --format "{{.State}}"
    else
        # nerdctl compose ps formats only as table or json.
        run_e -0 nerdctl compose --file "${compose_file}" --project-name rdd-compose ps --format json
        run -0 jq_output '.[].State'
    fi
    assert_output "running"
    ctrctl compose --file "${compose_file}" --project-name rdd-compose down
}

# assert_compose_logs asserts that a compose project's logs contain the text.
assert_compose_logs() { # <compose-file> <project> <text>
    run -0 ctrctl compose --file "$1" --project-name "$2" logs
    assert_output --partial "$3"
}

@test "compose logs returns service output" {
    create_host_tmpdir
    cat >"${HOST_TMPDIR}/compose.yaml" <<EOF
services:
  app:
    image: ${IMAGE_BUSYBOX}
    command: echo composed
EOF
    run -0 host_path "${HOST_TMPDIR}/compose.yaml"
    compose_file=${output}
    # nerdctl's up --abort-on-container-exit fails whenever a container exits,
    # so start detached and wait for the output to reach the logs.
    ctrctl compose --file "${compose_file}" --project-name rdd-compose-logs up --detach
    try --max 10 --delay 1 -- assert_compose_logs "${compose_file}" rdd-compose-logs composed
    ctrctl compose --file "${compose_file}" --project-name rdd-compose-logs down
}
