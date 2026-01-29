load '../../helpers/load'

# Notary external controller tests - tests notary controller running as external
# process. Verifies webhook functionality and resource processing.

# Note: This test requires bin/rdd-controller to be built.
# Run 'make build-rdd-controller' before running this test.

local_setup_file() {
    rdd svc delete
    rdd svc create --controllers=""
    rdd svc start
}

assert_process_exited() {
    local pid=$1
    # Return 0 (success) if process has exited, 1 (failure) if still running
    ! kill -0 "${pid}" 2>/dev/null
}

@test "control plane starts without embedded controllers" {
    # Confirm apiserver is working
    run -0 rdd ctl get namespaces -o name
    assert_line namespace/default

    # Verify that no embedded controller manager is running (fresh control plane)
    run -1 rdd ctl get configmap rdd-controller-manager --namespace rdd-system
    assert_output --partial "not found"
}

@test "external controller starts and registers" {
    # Start external rdd-controller binary in background, capturing logs for debugging
    "rdd-controller${EXE}" >"${BATS_FILE_TMPDIR}/controller1.log" 2>&1 &
    # Store PID to verify it auto-exits later
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"

    # Wait for external controller to register in discovery system
    try --max 20 --delay 1 -- rdd ctl get configmap rdd-controller-manager --namespace rdd-system

    # Verify the discovery ConfigMap contains notary controller
    run_e -0 rdd ctl get configmap rdd-controller-manager --namespace rdd-system -o jsonpath='{.data.rdd}'
    run -0 jq_output '.enabledControllers[]'
    assert_line "notary"
}

@test "webhook configuration is created" {
    # Wait for webhook configuration to be created by external controller
    try --max 20 --delay 1 -- rdd ctl get ValidatingWebhookConfiguration notary-validator

    # Verify webhook configuration structure
    run -0 rdd ctl get ValidatingWebhookConfiguration notary-validator -o jsonpath='{.webhooks[0].name}'
    assert_output "notary.rdd.rancherdesktop.io"

    run -0 rdd ctl get ValidatingWebhookConfiguration notary-validator -o json
    run -0 jq -r '.webhooks[0].clientConfig.url' <<<"${output}"
    assert_output --partial "https://127.0.0.1:"
    assert_output --partial "/validate-rdd-rancherdesktop-io-v1alpha1-notary"

    run -0 rdd ctl get ValidatingWebhookConfiguration notary-validator -o jsonpath='{.webhooks[0].failurePolicy}'
    assert_output "Fail"
}

@test "webhook rejects invalid resources" {
    # Test that validation works via webhook (external mode)
    cat >"${BATS_TMPDIR}/external-invalid-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-external-invalid
  namespace: default
spec:
  value: "invalid-external-test"
  configMapName: "test-config"
EOF

    # Apply the invalid resource - should be rejected by webhook
    run -1 rdd ctl apply -f "${BATS_TMPDIR}/external-invalid-notary.yaml"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"
    assert_output --partial "invalid-external-test"
}

@test "webhook accepts valid resources" {
    # Test that valid resources work via webhook
    cat >"${BATS_TMPDIR}/external-valid-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-external-valid
  namespace: default
spec:
  value: "valid-external-test"
  configMapName: "test-config"
EOF

    run -0 rdd ctl apply -f "${BATS_TMPDIR}/external-valid-notary.yaml"
    assert_output --partial "test-external-valid created"

    # Verify the resource was actually created
    run -0 rdd ctl get notary test-external-valid
    assert_output --partial "test-external-valid"
}

@test "controller processes notary resources" {
    # Wait for the controller to process the valid notary and create ConfigMap
    try --max 30 --delay 1 -- rdd ctl get configmap test-config
    # Verify ConfigMap was created with correct content
    run -0 rdd ctl get configmap test-config -o json
    run -0 jq -r '.data.change_000' <<<"${output}"
    assert_output --partial "value=valid-external-test"

    run -0 rdd ctl get configmap test-config -o jsonpath='{.metadata.labels.app\.kubernetes\.io/managed-by}'
    assert_output "notary-controller"

    # Verify notary status is updated
    run -0 rdd ctl get notary test-external-valid -o jsonpath='{.status.lastRecordedValue}'
    assert_output "valid-external-test"

    run -0 rdd ctl get notary test-external-valid -o jsonpath='{.status.configMapStatus}'
    assert_output "Updated"
}

@test "external controller auto-exits when control plane stops" {
    # Get the stored PID from the controller start test
    controller_pid=$(cat "${BATS_FILE_TMPDIR}/controller_pid")

    # Verify the external controller process is currently running
    run -0 kill -0 "${controller_pid}"

    # Stop the control plane - this should trigger auto-cleanup of external controllers
    trace "# Stopping control plane at $(date +%T)"
    run -0 rdd svc stop
    trace "# Control plane stopped at $(date +%T), waiting for controller exit"

    # Wait for external controller to detect control plane shutdown and exit
    # Worst case: 2s tick wait + 4s detection (2×2s intervals) + 10s manager shutdown + 5s cleanup = 21s
    # Use 30s to provide margin for slow CI machines
    if ! try --max 30 --delay 1 -- assert_process_exited "${controller_pid}"; then
        trace "# Controller did not exit in time. Log contents:"
        trace "$(cat "${BATS_FILE_TMPDIR}/controller1.log" || true)"
        return 1
    fi
    trace "# Controller exited at $(date +%T)"
}

@test "control plane starts with different controller" {
    rdd service delete
    rdd service create --controllers="lima"
    rdd service start
}

@test "control plane starts without rdd controller" {
    # Confirm apiserver is working
    run -0 rdd ctl get namespaces -o name
    assert_line namespace/default

    # Verify that the embedded lima controller is running
    run -0 rdd ctl get configmap rdd-controller-manager --namespace rdd-system --output jsonpath='{.data.embedded}'
    assert_output
    jq_output '.enabledControllers[]'
    assert_line "limavm"
    refute_line "notary"

    # Verify that the external rdd controller is not running
    run -0 rdd ctl get configmap rdd-controller-manager --namespace rdd-system --output jsonpath='{.data.rdd}'
    refute_output
}

@test "external controller runs and registers" {
    # Start external rdd-controller binary in background, capturing logs for debugging
    "rdd-controller${EXE}" >"${BATS_FILE_TMPDIR}/controller2.log" 2>&1 &
    # Store PID to verify it auto-exits later
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"

    # Wait for external controller to register in discovery system
    try --max 20 --delay 1 -- rdd ctl get configmap rdd-controller-manager --namespace rdd-system --allow-missing-template-keys=false --output jsonpath='{.data.rdd}'

    # Verify the discovery ConfigMap contains notary controller
    run -0 --separate-stderr rdd ctl get configmap rdd-controller-manager --namespace rdd-system --output jsonpath='{.data.rdd}'
    run -0 jq_output '.enabledControllers[]'
    assert_output
    assert_line "notary"
}

@test "shut down external controller" {
    # Get the stored PID from the controller start test
    controller_pid=$(cat "${BATS_FILE_TMPDIR}/controller_pid")

    # Verify the external controller process is currently running
    kill -0 "${controller_pid}"

    # Stop the control plane - this should trigger auto-cleanup of external controllers
    trace "# Stopping control plane at $(date +%T)"
    rdd service stop
    trace "# Control plane stopped at $(date +%T), waiting for controller exit"

    # Wait for external controller to detect control plane shutdown and exit
    # Worst case: 2s tick wait + 4s detection (2×2s intervals) + 10s manager shutdown + 5s cleanup = 21s
    # Use 30s to provide margin for slow CI machines
    if ! try --max 30 --delay 1 -- assert_process_exited "${controller_pid}"; then
        trace "# Controller did not exit in time. Log contents:"
        trace "$(cat "${BATS_FILE_TMPDIR}/controller2.log" || true)"
        return 1
    fi
    trace "# Controller exited at $(date +%T)"
}
