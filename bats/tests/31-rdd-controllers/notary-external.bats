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
    ! kill -0 "$pid" 2>/dev/null
}

@test "control plane starts without embedded controllers" {
    # Confirm apiserver is working
    run -0 rdd ctl get namespaces -o name
    assert_line namespace/default

    # Verify that no embedded controller manager is running (fresh control plane)
    run -1 rdd ctl get configmap rdd-controller-manager -n rdd-system
    assert_output --partial "not found"
}

@test "external controller starts and registers" {
    # Start external rdd-controller binary in background
    ../bin/rdd-controller &
    # Store PID to verify it auto-exits later
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"

    # Wait for external controller to register in discovery system
    try --max 20 --delay 1 -- rdd ctl get configmap rdd-controller-manager -n rdd-system

    # Verify the discovery ConfigMap contains notary controller
    run -0 rdd ctl get configmap rdd-controller-manager -n rdd-system -o jsonpath='{.data.enabledControllers}'
    assert_output --partial "notary"
}

@test "webhook configuration is created" {
    # Wait for webhook configuration to be created by external controller
    try --max 20 --delay 1 -- rdd ctl get validatingwebhookconfiguration notary-validator

    # Verify webhook configuration structure
    run -0 rdd ctl get validatingwebhookconfiguration notary-validator -o jsonpath='{.webhooks[0].name}'
    assert_output "notary.rdd.rancherdesktop.io"

    run -0 rdd ctl get validatingwebhookconfiguration notary-validator -o json
    run -0 jq -r '.webhooks[0].clientConfig.url' <<<"$output"
    assert_output --partial "https://127.0.0.1:"
    assert_output --partial "/validate-rdd-rancherdesktop-io-v1alpha1-notary"

    run -0 rdd ctl get validatingwebhookconfiguration notary-validator -o jsonpath='{.webhooks[0].failurePolicy}'
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
    run -0 jq -r '.data.change_000' <<<"$output"
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
    run -0 kill -0 "$controller_pid"

    # Stop the control plane - this should trigger auto-cleanup of external controllers
    run -0 rdd svc stop

    # Wait for external controller to detect control plane shutdown and exit
    # External controllers check every 2 seconds, allow up to 3 failures (6 seconds), plus buffer
    try --max 15 --delay 1 -- assert_process_exited "$controller_pid"
}
