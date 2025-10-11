load '../../helpers/load'

# LimaVM admission controller tests - tests webhook validation for cross-namespace
# uniqueness enforcement. LimaVM names must be unique across all namespaces
# because they correspond to actual VM instances on the host system.

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "test-ns1"
    rdd ctl create namespace "test-ns2"
    rdd ctl create namespace "test-ns3"
}

@test "webhook configuration has correct structure" {
    # Wait for the webhook configuration to be created
    try --max 20 --delay 3 -- rdd ctl get ValidatingWebHookConfiguration "limavm-validator"

    run -0 rdd ctl get ValidatingWebHookConfiguration limavm-validator -o jsonpath='{.webhooks[0]}'
    local json=$output

    run -0 jq_raw '.failurePolicy' "$json"
    assert_output "Fail"

    run -0 jq_raw '.name' "$json"
    assert_output "limavm.lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiGroups[0]' "$json"
    assert_output "lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiVersions[0]' "$json"
    assert_output "v1alpha1"

    run -0 jq_raw '.rules[0].resources[0]' "$json"
    assert_output "limavms"

    run -0 jq_raw '.rules[0].operations[]' "$json"
    assert_line "CREATE"
    assert_line "UPDATE"
}

@test "create LimaVM in first namespace succeeds" {
    run -0 rdd lima create "my-vm" -n "test-ns1"
    assert_output --partial "created"

    # Verify the LimaVM was created
    run -0 rdd ctl get limavm "my-vm" -n "test-ns1" -o name
    assert_line "limavm.lima.rancherdesktop.io/my-vm"
}

@test "duplicate LimaVM name in different namespace is rejected" {
    # Try to create LimaVM with same name in test-ns2
    run -1 rdd lima create "my-vm" -n "test-ns2"
    assert_output --partial "denied the request"
    assert_output --partial "already used"
    assert_output --partial "unique across all namespaces"

    # Verify the LimaVM was NOT created in test-ns2
    run -1 rdd ctl get limavm "my-vm" -n "test-ns2"
    assert_output --partial "not found"
}

@test "dry-run validates duplicate names without creating resource" {
    # Try to create duplicate name with dry-run=server
    run -1 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: my-vm
  namespace: test-ns2
spec: {}
EOF
    assert_output --partial "Forbidden"
    assert_output --partial "already used"

    # Verify the resource was not created (dry-run should never create)
    run -1 rdd ctl get limavm "my-vm" -n "test-ns2"
    assert_output --partial "not found"
}

@test "deletion allows recreation in different namespace" {
    # Delete the LimaVM from test-ns1
    run -0 rdd lima delete "my-vm"
    assert_output --partial "deleted"

    # Verify deletion succeeded
    run -1 rdd ctl get limavm "my-vm" -n "test-ns1"
    assert_output --partial "not found"

    # Now we should be able to create a LimaVM with the same name in test-ns2
    run -0 rdd lima create "my-vm" -n "test-ns2"
    assert_output --partial "created"

    # Verify the LimaVM was created in test-ns2
    run -0 rdd ctl get limavm "my-vm" -n "test-ns2" -o name
    assert_line "limavm.lima.rancherdesktop.io/my-vm"
}

@test "multiple unique VMs across namespaces are allowed" {
    # Create several LimaVMs with unique names across different namespaces
    run -0 rdd lima create "vm1" -n "test-ns1"
    assert_output --partial "created"

    run -0 rdd lima create "vm2" -n "test-ns2"
    assert_output --partial "created"

    run -0 rdd lima create "vm3" -n "test-ns3"
    assert_output --partial "created"

    # Verify all VMs exist
    run -0 rdd ctl get limavms -A
    assert_output --partial "vm1"
    assert_output --partial "vm2"
    assert_output --partial "vm3"
}
