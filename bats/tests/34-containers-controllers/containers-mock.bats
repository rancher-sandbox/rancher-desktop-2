load '../../helpers/load'

# Mock controller tests - using the mock controller, verify that the container
# and image controllers work as expected.

TEST_DATA_PATH="${PATH_BATS_ROOT}/../pkg/controllers/mock/testdata"
NAMESPACE="rancher-desktop"

local_setup_file() {
    setup_rdd_control_plane "containers"
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
