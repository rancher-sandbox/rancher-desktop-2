load '../../helpers/load'

local_setup_file() {
    # Use the local rdd binary from the project (use absolute path to avoid relative path issues)
    export RDD_FILE="${BATS_FILE_TMPDIR}/rdd${EXE}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${RDD_FILE}"
}
# TODO check for developer mode when running inside the repo checkout

@test 'RDD_DEVELOPER_MODE=0' {
    run -0 env RDD_DEVELOPER_MODE=0 "${RDD_FILE}" svc status
    refute_line --partial "developer mode"
}

@test 'RDD_DEVELOPER_MODE=false' {
    run -0 env RDD_DEVELOPER_MODE=false "${RDD_FILE}" svc status
    refute_line --partial "developer mode"
}

@test 'RDD_DEVELOPER_MODE=1' {
    run -0 env RDD_DEVELOPER_MODE=1 "${RDD_FILE}" svc status
    assert_line --partial "developer mode"
}

@test 'RDD_DEVELOPER_MODE=true' {
    run -0 env RDD_DEVELOPER_MODE=true "${RDD_FILE}" svc status
    assert_line --partial "developer mode"
}
