load '../../helpers/load'

# LimaVM admission controller tests - tests webhook validation for cross-namespace
# uniqueness enforcement. LimaVM names must be unique across all namespaces
# because they correspond to actual VM instances on the host system.

# non-functional template, but passes Lima validation
TEMPLATE='images: [{"location":"https://foo.test."}]'

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "test-ns1"
    rdd ctl create namespace "test-ns2"
    rdd ctl create namespace "test-ns3"

    rdd ctl create configmap "test-template" --namespace "test-ns1" --from-literal="template=${TEMPLATE}"
    rdd ctl create configmap "test-template" --namespace "test-ns2" --from-literal="template=${TEMPLATE}"
    rdd ctl create configmap "test-template" --namespace "test-ns3" --from-literal="template=${TEMPLATE}"
}

@test "webhook configuration has correct structure" {
    # Wait for the mutating webhook configuration to be created
    rdd ctl wait --for=create MutatingWebhookConfiguration "limavm-defaulter" --timeout=60s

    run -0 rdd ctl get MutatingWebhookConfiguration limavm-defaulter -o jsonpath='{.webhooks[0]}'
    local json=${output}

    run -0 jq_raw '.failurePolicy' "${json}"
    assert_output "Fail"

    run -0 jq_raw '.name' "${json}"
    assert_output "limavm-defaulter.lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiGroups[0]' "${json}"
    assert_output "lima.rancherdesktop.io"

    run -0 jq_raw '.rules[0].apiVersions[0]' "${json}"
    assert_output "v1alpha1"

    run -0 jq_raw '.rules[0].resources[0]' "${json}"
    assert_output "limavms"

    run -0 jq_raw '.rules[0].operations[0]' "${json}"
    assert_output "CREATE"
}

@test "create LimaVM in first namespace succeeds" {
    run -0 rdd lima create "my-vm" "test-template" --namespace "test-ns1"
    assert_output --partial "created"

    # Verify the LimaVM was created
    run -0 rdd ctl get limavm "my-vm" --namespace "test-ns1" -o name
    assert_line "limavm.lima.rancherdesktop.io/my-vm"
}

@test "duplicate LimaVM name in different namespace is rejected" {
    # Try to create LimaVM with same name in test-ns2
    run -1 rdd lima create "my-vm" "test-template" --namespace "test-ns2"
    assert_output --partial "denied the request"
    assert_output --partial "already used"
    assert_output --partial "unique across all namespaces"

    # Verify the LimaVM was NOT created in test-ns2
    run -1 rdd ctl get limavm "my-vm" --namespace "test-ns2"
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
spec:
  templateRef:
    name: test-template
    namespace: test-ns2
  running: false
EOF
    assert_output --partial "Forbidden"
    assert_output --partial "already used"

    # Verify the resource was not created (dry-run should never create)
    run -1 rdd ctl get limavm "my-vm" --namespace "test-ns2"
    assert_output --partial "not found"
}

@test "dry-run validates template data without creating ConfigMap copy" {
    # Create a ConfigMap with empty template data (invalid)
    run -0 rdd ctl create configmap "invalid-template" --namespace "test-ns3" --from-literal=template=''

    # Try to create LimaVM with empty template using dry-run=server
    run -1 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: test-invalid-vm
  namespace: test-ns3
spec:
  templateRef:
    name: invalid-template
  running: false
EOF
    assert_output --partial "denied the request"
    assert_output --partial '"template" data cannot be empty'

    # Verify the LimaVM was not created (dry-run should never create)
    run -1 rdd ctl get limavm "test-invalid-vm" --namespace "test-ns3"
    assert_output --partial "not found"

    # Verify that no ConfigMap copy was created during dry-run
    run -1 rdd ctl get configmap "test-invalid-vm-template" --namespace "test-ns3"
    assert_output --partial "not found"
}

@test "dry-run detects pre-existing ConfigMap name conflict" {
    # Create an unrelated ConfigMap that happens to use the name we need
    rdd ctl create configmap "test-vm-template" --namespace "test-ns3" --from-literal="template=${TEMPLATE}"
    rdd ctl get configmap "test-vm-template" --namespace "test-ns3"

    # Try to create a LimaVM that would try to create the same ConfigMap with dry-run=server
    # This should fail because a ConfigMap with that name already exists
    run -1 rdd ctl create -f - --dry-run=server <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: test-vm
  namespace: test-ns3
spec:
  templateRef:
    name: test-template
  running: false
EOF
    assert_output --partial "already exists"
    assert_output --partial "test-vm-template"

    # Make sure the unrelated Configmap still exists
    rdd ctl get configmap "test-vm-template" --namespace "test-ns3"

    # Verify the actual create also fails
    run -1 rdd ctl create -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: test-vm
  namespace: test-ns3
spec:
  templateRef:
    name: test-template
  running: false
EOF
    assert_output --partial "already exists"
    assert_output --partial "test-vm-template"
}

@test "deletion allows recreation in different namespace" {
    # Delete the LimaVM from test-ns1
    run -0 rdd lima delete "my-vm"
    assert_output --partial "deleted"

    # Wait for deletion to complete (asynchronous Kubernetes deletion)
    rdd ctl wait --for=delete limavm "my-vm" --namespace "test-ns1" --timeout=10s

    # Now we should be able to create a LimaVM with the same name in test-ns2
    run -0 rdd lima create "my-vm" "test-template" --namespace "test-ns2"
    assert_output --partial "created"

    # Verify the LimaVM was created in test-ns2
    run -0 rdd ctl get limavm "my-vm" --namespace "test-ns2" -o name
    assert_line "limavm.lima.rancherdesktop.io/my-vm"
}

@test "multiple unique VMs across namespaces are allowed" {
    # Create several LimaVMs with unique names across different namespaces
    run -0 rdd lima create "vm1" "test-template" --namespace "test-ns1"
    assert_output --partial "created"

    run -0 rdd lima create "vm2" "test-template" --namespace "test-ns2"
    assert_output --partial "created"

    run -0 rdd lima create "vm3" "test-template" --namespace "test-ns3"
    assert_output --partial "created"

    # Verify all VMs exist
    run -0 rdd ctl get limavms -A
    assert_output --partial "vm1"
    assert_output --partial "vm2"
    assert_output --partial "vm3"
}
