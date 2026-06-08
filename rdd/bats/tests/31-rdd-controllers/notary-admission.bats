load '../../helpers/load'

# Notary admission controller tests - tests webhook validation, rejection,
# warnings, and dry-run functionality. For core controller functionality
# like resource processing and ConfigMap creation, see notary.bats

local_setup_file() {
    setup_rdd_control_plane "notary"
}

# create_notary_yaml <name> <value>
create_notary_yaml() {
    local name=$1
    local value=$2

    cat <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: ${name}
  namespace: default
spec:
  value: "${value}"
  configMapName: test-config
EOF
}

# apply_notary <name> <value>
apply_notary() {
    local name=$1
    local value=$2

    run -0 create_notary_yaml "${name}" "${value}"
    rdd ctl apply -f - <<<"${output}"
}

@test "webhook configuration has correct structure" {
    # Wait for the webhook configuration to be created
    rdd ctl wait --for=create ValidatingWebhookConfiguration notary-validator --timeout=60s

    run -0 rdd ctl get ValidatingWebhookConfiguration notary-validator -o jsonpath='{.webhooks[0]}'
    local json=${output}

    run -0 jq_raw '.failurePolicy' "${json}"
    assert_output "Fail"

    run -0 jq_raw '.name' "${json}"
    assert_output "notary.rdd.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiGroups[0]' "${json}"
    assert_output "rdd.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiVersions[0]' "${json}"
    assert_output "v1alpha1"

    run -0 jq_raw '.rules[0].resources[0]' "${json}"
    assert_output "notaries"

    run -0 jq_raw '.rules[0].operations[]' "${json}"
    assert_line "CREATE"
    assert_line "UPDATE"
}

@test "invalid resource is rejected" {
    run -1 apply_notary "test-invalid-notary" "INVALID-value"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"
    assert_output --partial "INVALID-value"
}

@test "edge case values are handled correctly" {
    # Test value that contains "invalid" but doesn't start with it
    run -0 apply_notary "test-contains-invalid" "not-invalid-but-contains-it"
    assert_output --partial "test-contains-invalid created"
}

@test 'empty value is valid' {
    run -0 apply_notary "test-empty-value" ""
    assert_output --partial "test-empty-value created"
}

@test "warnings are shown for long values" {
    # Create a resource with a value longer than 24 characters
    run -0 apply_notary "test-long-value-notary" "this-is-a-very-long-value-that-exceeds-24-characters"
    assert_output --partial "test-long-value-notary created"
    assert_output --partial "longer than 24 characters"
}

@test "dry-run=server rejects invalid values" {
    # Create an invalid notary resource for dry-run testing
    run -0 create_notary_yaml "test-dry-run-server-invalid" "invalid-dry-run-test"
    run -1 rdd ctl apply --dry-run=server -f - <<<"${output}"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"

    # Verify the resource was not created (should not exist anyway due to validation failure)
    run -1 rdd ctl get notary test-dry-run-server-invalid
    assert_output --partial "not found"
}

@test "dry-run=server update rejects invalid values" {
    # First create an actual resource with valid value
    run -0 apply_notary "test-dry-run-update-invalid" "valid-original-value"
    assert_output --partial "test-dry-run-update-invalid created"

    # Verify resource was created
    run -0 rdd ctl get notary test-dry-run-update-invalid -o jsonpath='{.spec.value}'
    assert_output "valid-original-value"

    # Apply invalid update with --dry-run=server - should be rejected by admission controllers
    run -0 create_notary_yaml "test-dry-run-update-invalid" "invalid-updated-value"
    run -1 rdd ctl apply --dry-run=server -f - <<<"${output}"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"

    # Verify the resource was not updated and still has the original valid value
    run -0 rdd ctl get notary test-dry-run-update-invalid -o jsonpath='{.spec.value}'
    assert_output "valid-original-value"
}
