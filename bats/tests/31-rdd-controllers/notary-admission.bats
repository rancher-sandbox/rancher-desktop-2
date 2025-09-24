load '../../helpers/load'

# Notary admission controller tests - tests webhook validation, rejection,
# warnings, and dry-run functionality. For core controller functionality
# like resource processing and ConfigMap creation, see notary.bats

local_setup_file() {
    setup_rdd_control_plane "notary"
}

@test "webhook configuration is created" {
    # Wait for the webhook configuration to be created
    try --max 20 --delay 3 -- rdd ctl get validatingwebhookconfiguration notary-validator

    # Check that the webhook configuration exists
    run -0 rdd ctl get validatingwebhookconfiguration notary-validator
    assert_output --partial "notary-validator"
}

@test "webhook URL is properly configured" {
    run -0 rdd ctl get validatingwebhookconfiguration notary-validator -o jsonpath='{.webhooks[0].clientConfig.url}'
    assert_output --partial "https://127.0.0.1:"
    assert_output --partial "/validate-rdd-rancherdesktop-io-v1alpha1-notary"
}

@test "webhook configuration has correct structure" {
    run -0 rdd ctl get validatingwebhookconfiguration notary-validator -o jsonpath='{.webhooks[0]}'
    local json=$output

    run -0 jq -r '.failurePolicy' <<<"$output"
    assert_output "Fail"

    run -0 jq -r ".name" <<<"$json"
    assert_output "notary.rdd.rancherdesktop.io"

    run -0 jq -r '.rules[0].apiGroups[0]' <<<"$json"
    assert_output "rdd.rancherdesktop.io"

    run -0 jq -r '.rules[0].apiVersions[0]' <<<"$json"
    assert_output "v1alpha1"

    run -0 jq -r '.rules[0].resources[0]' <<<"$json"
    assert_output "notaries"

    run -0 jq -r '.rules[0].operations[]' <<<"$json"
    assert_line "CREATE"
    assert_line "UPDATE"
}

@test "invalid resource is rejected" {
    cat >"${BATS_TMPDIR}/invalid-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-invalid-notary
  namespace: default
spec:
  value: "invalid-value"
  configMapName: "test-config"
EOF
    run -1 rdd ctl apply -f "${BATS_TMPDIR}/invalid-notary.yaml"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"
    assert_output --partial "invalid-value"
}

@test "case-insensitive validation works" {
    # Test "Invalid" (capital I)
    cat >"${BATS_TMPDIR}/invalid-case1.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-invalid-case1
  namespace: default
spec:
  value: "Invalid-test"
  configMapName: "test-config"
EOF
    run -1 rdd ctl apply -f "${BATS_TMPDIR}/invalid-case1.yaml"
    assert_output --partial "Forbidden"
    assert_output --partial "Invalid-test"
}

@test "edge case values are handled correctly" {
    # Test value that contains "invalid" but doesn't start with it
    cat >"${BATS_TMPDIR}/contains-invalid.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-contains-invalid
  namespace: default
spec:
  value: "not-invalid-but-contains-it"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/contains-invalid.yaml"
    assert_output --partial "test-contains-invalid created"

    # Test empty value
    cat >"${BATS_TMPDIR}/empty-value.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-empty-value
  namespace: default
spec:
  value: ""
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/empty-value.yaml"
    assert_output --partial "test-empty-value created"
}

@test "warnings are shown for long values" {
    # Create a resource with a value longer than 24 characters
    cat >"${BATS_TMPDIR}/long-value-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-long-value-notary
  namespace: default
spec:
  value: "this-is-a-very-long-value-that-exceeds-24-characters"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/long-value-notary.yaml"
    assert_output --partial "test-long-value-notary created"

    assert_output --partial "longer than 24 characters"
}

@test "no warnings for short values" {
    # Create a resource with a value shorter than 24 characters
    cat >"${BATS_TMPDIR}/short-value-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-short-value-notary
  namespace: default
spec:
  value: "short-value"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/short-value-notary.yaml"
    assert_output --partial "test-short-value-notary created"

    refute_output --partial "longer than 24 characters"
}

@test "dry-run=client validation works" {
    # Create a valid notary resource for dry-run testing
    cat >"${BATS_TMPDIR}/dry-run-client-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-client
  namespace: default
spec:
  value: "dry-run-test-value"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply --dry-run=client -f "${BATS_TMPDIR}/dry-run-client-notary.yaml"
    assert_output --partial "test-dry-run-client created (dry run)"

    # Verify the resource was not actually created
    run -1 rdd ctl get notary test-dry-run-client
    assert_output --partial "not found"
}

@test "dry-run=server validation works" {
    # Create a valid notary resource for dry-run testing
    cat >"${BATS_TMPDIR}/dry-run-server-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-server
  namespace: default
spec:
  value: "dry-run-server-test-value"
  configMapName: "test-config"
EOF
    # Apply with --dry-run=server (server-side validation including admission controllers)
    run -0 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/dry-run-server-notary.yaml"
    assert_output --partial "test-dry-run-server created (server dry run)"

    # Verify the resource was not actually created
    run -1 rdd ctl get notary test-dry-run-server
    assert_output --partial "not found"
}

@test "dry-run=server rejects invalid values" {
    # Create an invalid notary resource for dry-run testing
    cat >"${BATS_TMPDIR}/dry-run-server-invalid.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-server-invalid
  namespace: default
spec:
  value: "invalid-dry-run-test"
  configMapName: "test-config"
EOF
    run -1 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/dry-run-server-invalid.yaml"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"

    # Verify the resource was not created (should not exist anyway due to validation failure)
    run -1 rdd ctl get notary test-dry-run-server-invalid
    assert_output --partial "not found"
}

@test "dry-run=server shows warnings for long values" {
    # Create a resource with a long value for dry-run testing
    cat >"${BATS_TMPDIR}/dry-run-server-long.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-server-long
  namespace: default
spec:
  value: "this-is-a-very-long-dry-run-value-that-exceeds-24-characters"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/dry-run-server-long.yaml"
    assert_output --partial "test-dry-run-server-long created (server dry run)"

    assert_output --partial "longer than 24 characters"

    # Verify the resource was not actually created
    run -1 rdd ctl get notary test-dry-run-server-long
    assert_output --partial "not found"
}

@test "dry-run=client update validation works" {
    cat >"${BATS_TMPDIR}/original-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-client
  namespace: default
spec:
  value: "original-value"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/original-notary.yaml"
    assert_output --partial "test-dry-run-update-client created"

    # Get the original value to verify it doesn't change
    run -0 rdd ctl get notary test-dry-run-update-client -o jsonpath='{.spec.value}'
    assert_output "original-value"

    cat >"${BATS_TMPDIR}/updated-notary.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-client
  namespace: default
spec:
  value: "updated-value"
  configMapName: "test-config"
EOF
    # Apply update with --dry-run=client (client-side validation only)
    run -0 rdd ctl apply --dry-run=client -f "${BATS_TMPDIR}/updated-notary.yaml"
    assert_output --partial "test-dry-run-update-client configured (dry run)"

    # Verify the resource was not actually updated (still has original value)
    run -0 rdd ctl get notary test-dry-run-update-client -o jsonpath='{.spec.value}'
    assert_output "original-value"
}

@test "dry-run=server update validation works" {
    cat >"${BATS_TMPDIR}/original-notary-server.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-server
  namespace: default
spec:
  value: "original-server-value"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/original-notary-server.yaml"
    assert_output --partial "test-dry-run-update-server created"

    run -0 rdd ctl get notary test-dry-run-update-server -o jsonpath='{.spec.value}'
    assert_output "original-server-value"

    cat >"${BATS_TMPDIR}/updated-notary-server.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-server
  namespace: default
spec:
  value: "updated-server-value"
  configMapName: "test-config"
EOF
    # Apply update with --dry-run=server (server-side validation including admission controllers)
    run -0 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/updated-notary-server.yaml"
    assert_output --partial "test-dry-run-update-server configured (server dry run)"

    # Verify the resource was not actually updated (still has original value)
    run -0 rdd ctl get notary test-dry-run-update-server -o jsonpath='{.spec.value}'
    assert_output "original-server-value"
}

@test "dry-run=server update rejects invalid values" {
    # First create an actual resource with valid value
    cat >"${BATS_TMPDIR}/valid-original.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-invalid
  namespace: default
spec:
  value: "valid-original-value"
  configMapName: "test-config"
EOF

    run -0 rdd ctl apply -f "${BATS_TMPDIR}/valid-original.yaml"
    assert_output --partial "test-dry-run-update-invalid created"

    run -0 rdd ctl get notary test-dry-run-update-invalid -o jsonpath='{.spec.value}'
    assert_output "valid-original-value"

    cat >"${BATS_TMPDIR}/invalid-update.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-invalid
  namespace: default
spec:
  value: "invalid-updated-value"
  configMapName: "test-config"
EOF
    # Apply invalid update with --dry-run=server - should be rejected by admission controllers
    run -1 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/invalid-update.yaml"
    assert_output --partial "Forbidden"
    assert_output --partial "spec.value cannot start with 'invalid' (case-insensitive)"

    # Verify the resource was not updated and still has the original valid value
    run -0 rdd ctl get notary test-dry-run-update-invalid -o jsonpath='{.spec.value}'
    assert_output "valid-original-value"
}

@test "dry-run=server update shows warnings for long values" {
    # First create an actual resource with short value
    cat >"${BATS_TMPDIR}/short-original.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-warn
  namespace: default
spec:
  value: "short-value"
  configMapName: "test-config"
EOF
    run -0 rdd ctl apply -f "${BATS_TMPDIR}/short-original.yaml"
    assert_output --partial "test-dry-run-update-warn created"

    run -0 rdd ctl get notary test-dry-run-update-warn -o jsonpath='{.spec.value}'
    assert_output "short-value"

    cat >"${BATS_TMPDIR}/long-update.yaml" <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-dry-run-update-warn
  namespace: default
spec:
  value: "this-is-a-very-long-updated-value-that-exceeds-24-characters-and-should-warn"
  configMapName: "test-config"
EOF
    # Apply long value update with --dry-run=server - should succeed with warnings
    run -0 rdd ctl apply --dry-run=server -f "${BATS_TMPDIR}/long-update.yaml"
    assert_output --partial "test-dry-run-update-warn configured (server dry run)"

    assert_output --partial "longer than 24 characters"

    # Verify the resource was not actually updated (still has original short value)
    run -0 rdd ctl get notary test-dry-run-update-warn -o jsonpath='{.spec.value}'
    assert_output "short-value"
}
