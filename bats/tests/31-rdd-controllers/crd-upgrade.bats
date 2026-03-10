load '../../helpers/load'

# Verify that the controller updates existing CRDs on startup.
# Applies a notary CRD with a missing field, starts the controller,
# and confirms the schema is updated.

# Note: This test requires bin/rdd-controller to be built.
# Run 'make build-rdd-controller' before running this test.

local_setup_file() {
    rdd svc delete
    rdd svc start
}

local_teardown_file() {
    if [[ -f "${BATS_FILE_TMPDIR}/controller_pid" ]]; then
        kill "$(cat "${BATS_FILE_TMPDIR}/controller_pid")" 2>/dev/null || true
    fi
    rdd svc delete
}

assert_process_exited() {
    local pid=$1
    ! kill -0 "${pid}" 2>/dev/null
}

assert_crd_has_change_count() {
    local expected=$1
    run_e -0 rdd ctl get crd notaries.rdd.rancherdesktop.io --output json
    run -0 jq_output '.spec.versions[0].schema.openAPIV3Schema.properties.status.properties | has("changeCount")'
    assert_output "${expected}"
}

@test "controllers install CRDs" {
    rdd ctl wait crd notaries.rdd.rancherdesktop.io --for create --timeout=20s
    rdd ctl wait crd notaries.rdd.rancherdesktop.io --for condition=Established --timeout=10s
}

@test "extract CRD and remove changeCount field" {
    # Strip server-managed metadata so the CRD can be re-applied to a fresh
    # control plane, and remove changeCount to simulate an old schema.
    run_e -0 rdd ctl get crd notaries.rdd.rancherdesktop.io --output json
    run -0 jq_output 'del(
        .metadata.resourceVersion,
        .metadata.uid,
        .metadata.creationTimestamp,
        .metadata.generation,
        .metadata.managedFields,
        .status,
        .spec.versions[0].schema.openAPIV3Schema.properties.status.properties.changeCount
    )'
    save_var output
}

@test "restart control plane without controllers" {
    rdd svc delete
    rdd svc start --controllers=""
    run -0 rdd ctl get namespaces --output name
    assert_line namespace/default
}

@test "apply old CRD without changeCount field" {
    load_var output
    echo "${output}" | rdd ctl apply --filename -
    rdd ctl wait crd notaries.rdd.rancherdesktop.io --for condition=Established --timeout=10s
}

@test "old CRD schema lacks changeCount" {
    assert_crd_has_change_count false
}

@test "external controller updates CRD on startup" {
    "rdd-controller${EXE}" &>"${RDD_LOG_DIR}/rdd-controller.log" &
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"

    # Wait for the controller to start and update the CRD schema
    try --max 30 --delay 1 -- assert_crd_has_change_count true
}

@test "controller shuts down with control plane" {
    controller_pid=$(cat "${BATS_FILE_TMPDIR}/controller_pid")
    kill -0 "${controller_pid}"

    trace "# Stopping control plane at $(date +%T)"
    rdd svc stop
    trace "# Control plane stopped at $(date +%T), waiting for controller exit"

    if ! try --max 30 --delay 1 -- assert_process_exited "${controller_pid}"; then
        trace "# Controller did not exit in time. Log contents:"
        trace "$(cat "${RDD_LOG_DIR}/rdd-controller.log" || true)"
        return 1
    fi
    trace "# Controller exited at $(date +%T)"
}
