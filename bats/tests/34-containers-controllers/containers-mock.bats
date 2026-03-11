load '../../helpers/load'

# Mock controller tests - using the mock controller, verify that the container
# and image controllers work as expected.

TEST_DATA_PATH="${PATH_BATS_ROOT}/../pkg/controllers/mock/testdata"

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

    run -0 cat "${TEST_DATA_PATH}/containers.json"
    run -0 jq_output '.[].Id'
    containers=${output}

    while IFS= read -r container; do
        rdd ctl wait --for=create container "${container}" --timeout=30s
    done <<<"${containers}"
}

@test "images are created" {
    rdd ctl wait --for=create namespace rdd-mocks --timeout=30s

    run -0 cat "${TEST_DATA_PATH}/images.json"
    run -0 jq_output '.[].RepoTags.[]'
    images=${output}

    while IFS= read -r image; do
        try --max 30 --delay 1 -- rdd ctl get image --field-selector "status.repoTag=${image}" --output jsonpath='{.items[*].status.repoTag}'
        assert_line "${image}"
    done <<<"${images}"
}

@test "volumes are created" {
    rdd ctl wait --for=create namespace rdd-mocks --timeout=30s

    run -0 cat "${TEST_DATA_PATH}/volumes.json"
    run -0 jq_output '.[].Name'
    volumes=${output}

    while IFS= read -r volume; do
        rdd ctl wait --for=create volume "${volume}" --timeout=30s
    done <<<"${volumes}"
}
