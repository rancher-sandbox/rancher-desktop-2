# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Mock controller tests - using the mock controller, verify that the container
# and image controllers work as expected.

TEST_DATA_PATH="${PATH_BATS_ROOT}/../pkg/controllers/mock/testdata"
NAMESPACE="rancher-desktop"

local_setup_file() {
    # -engine keeps the engine controller out: it would race the
    # external mock-controller on the shared Container/Image/Volume
    # mirrors in the rancher-desktop namespace.
    setup_rdd_control_plane "containers,-engine"
    echo "${RDD_LOG_DIR}/mock-controller.log" >&3
    "mock-controller${EXE}" &>"${RDD_LOG_DIR}/mock-controller.log" &
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"
}

local_teardown_file() {
    if [[ -f "${BATS_FILE_TMPDIR}/controller_pid" ]]; then
        read -r controller_pid <"${BATS_FILE_TMPDIR}/controller_pid"
        kill "${controller_pid}" 2>/dev/null || true
        wait "${controller_pid}" 2>/dev/null || true
    fi
}

@test "containers are created" {
    rdd ctl wait --for=create namespace rdd-mocks --timeout=30s
    rdd ctl wait --for=create namespace "${NAMESPACE}" --timeout=30s

    run -0 cat "${TEST_DATA_PATH}/containers.json"
    run -0 jq_output '.[].Id'
    mapfile -t containers <<<"${output}"

    rdd ctl wait --for=create --namespace="${NAMESPACE}" container "${containers[@]}" --timeout=30s
}

@test "container logs can be fetched" {
    # Make sure everything we need exists
    rdd ctl wait --for=create namespace/rdd-mocks --timeout=30s
    rdd ctl wait --for=create --namespace rdd-system configmap/rdd-controller-manager --timeout=30s

    # Check that the mock controller has a containers/logs passthrough.
    run -0 rdd ctl get --namespace rdd-system configmap/rdd-controller-manager -o jsonpath='{.data.mock}'
    run -0 jq_output '.enabledPassthroughs.mock[]'
    assert_line logs

    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi

    # Grab a random container and check its logs.
    run -0 cat "${TEST_DATA_PATH}/containers.json"
    container_data=${output}
    run -0 jq_raw '.[0].Id' "${container_data}"
    container=${output}
    label=org.opencontainers.image.source
    run -0 jq_raw ".[0].Config.Labels[\"${label}\"]" "${container_data}"
    value=${output}

    # Set up curl to fetch logs.
    run -0 rdd ctl config view --minify --flatten --output=jsonpath='{.clusters[].cluster.server}'
    local server_url=${output}
    run -0 rdd ctl config view --minify --flatten --output=jsonpath='{.users[].user.token}'
    local token=${output}
    run -0 curl --silent --header "Authorization: Bearer ${token}" --insecure \
        "${server_url/http/ws}/passthrough/mock/logs/${container}"
    assert_line "Logs for container ${container}"
    assert_line "Label: ${label}"$'\t'"${value}"

    # Check that invalid containers are rejected.
    run -22 curl --silent --header "Authorization: Bearer ${token}" --insecure \
        "${server_url/http/ws}/passthrough/mock/logs/invalid-container-id"
    run -22 curl --silent --header "Authorization: Bearer ${token}" --insecure \
        "${server_url/http/ws}/passthrough/mock/logs/00000000"
}

@test "images are created" {
    rdd ctl wait --for=create namespace rdd-mocks --timeout=30s

    run -0 cat "${TEST_DATA_PATH}/images.json"
    run -0 jq_output '.[].RepoTags.[]'
    images=${output}

    while IFS= read -r image; do
        rdd ctl wait --for=create --namespace="${NAMESPACE}" image \
            --field-selector "status.repoTag=${image}" --timeout=30s
    done <<<"${images}"
}

@test "volumes are created" {
    rdd ctl wait --for=create namespace rdd-mocks --timeout=30s

    run -0 cat "${TEST_DATA_PATH}/volumes.json"
    run -0 jq_output '.[].Name'
    mapfile -t volumes <<<"${output}"

    rdd ctl wait --for=create --namespace="${NAMESPACE}" volume "${volumes[@]}" --timeout=30s
}
