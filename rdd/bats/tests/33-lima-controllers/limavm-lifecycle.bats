load '../../helpers/load'

# LimaVM lifecycle tests the complete lifecycle of LimaVM resources,
# including template ConfigMap creation, protection, modification validation,
# and automatic cleanup on deletion.

NAMESPACE="lifecycle-test-ns"
VM_NAME="test-vm"
TEMPLATE_NAME="${VM_NAME}-template"

# non-functional template, but passes Lima validation
TEMPLATE='images: [{"location":"https://foo.test."}]'

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "${NAMESPACE}"
}

# Helper function to create a LimaVM
create_limavm() {
    local name=$1
    local template=$2

    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
spec:
  templateRef:
    name: ${template}
    namespace: ${NAMESPACE}
  running: false
EOF
}

@test "create source-template ConfigMap" {
    rdd ctl create configmap "source-template" --namespace "${NAMESPACE}" --from-literal="template=${TEMPLATE}"

    # Verify the template was created
    run -0 rdd ctl get configmap "source-template" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output "${TEMPLATE}"
}

@test "create LimaVM resource" {
    create_limavm "${VM_NAME}" "source-template"

    # Verify the LimaVM was created
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" -o name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "verify LimaVM has finalizer for cleanup" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test "wait for template ConfigMap to be created" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout="30s"
}

@test "verify copied ConfigMap has correct data" {
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output "${TEMPLATE}"
}

@test "verify template ConfigMap has correct label" {
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        -o jsonpath='{.metadata.labels.lima\.rancherdesktop\.io/template-configmap}'
    assert_output "true"
}

@test "verify template ConfigMap has owned finalizer" {
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/owned-by-LimaVM"
}

@test "verify template ConfigMap has owner reference" {
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o json
    local json=${output}

    run -0 jq -r '.metadata.ownerReferences[0].kind' <<<"${json}"
    assert_output "LimaVM"

    run -0 jq -r '.metadata.ownerReferences[0].name' <<<"${json}"
    assert_output "${VM_NAME}"

    run -0 jq -r '.metadata.ownerReferences[0].controller' <<<"${json}"
    assert_output "true"
}

@test "verify LimaVM status has template ConfigMap name" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.status.templateConfigMap}'
    assert_output "${TEMPLATE_NAME}"
}

@test "template ConfigMap modification is allowed if template key exists" {
    run -0 rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='merge' \
        --patch='{"data":{"template":"images: [{\"location\":\"https://bar.test.\"}]"}}'
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output 'images: [{"location":"https://bar.test."}]'
}

@test "template ConfigMap modification without template key is rejected" {
    # Capture current state before attempting invalid modification
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    local template_before="${output}"

    run -1 rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='json' \
        --patch='[{"op":"remove","path":"/data/template"}]'
    assert_output --partial "Forbidden"
    assert_output --partial 'template ConfigMap must have a "template" data entry'

    # Verify template data is unchanged after rejected patch
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output "${template_before}"
}

@test "template ConfigMap modification with empty template is rejected" {
    # Capture current state before attempting invalid modification
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    local template_before="${output}"

    run -1 rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='merge' \
        --patch='{"data":{"template":""}}'
    assert_output --partial "Forbidden"
    assert_output --partial '"template" data cannot be empty'

    # Verify template data is unchanged after rejected patch
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output "${template_before}"
}

@test "template ConfigMap label removal is rejected while owned" {
    run -1 rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='merge' \
        --patch='{"metadata":{"labels":{"lima.rancherdesktop.io/template-configmap":null}}}'
    assert_output --partial "Forbidden"
    assert_output --partial "cannot remove"
    assert_output --partial "resource is owned"
}

@test "template ConfigMap cannot be deleted independently" {
    run -1 rdd ctl delete configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --grace-period=0
    assert_output --partial "Forbidden"
    assert_output --partial "owned by LimaVM"
    assert_output --partial "delete the LimaVM resource instead"

    # Verify the ConfigMap still exists
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}"
    assert_output --partial "${TEMPLATE_NAME}"
}

@test "dry-run deletion of template ConfigMap is also rejected" {
    run -1 rdd ctl delete configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --dry-run=server
    assert_output --partial "Forbidden"
    assert_output --partial "owned by LimaVM"

    # Verify the ConfigMap still exists
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}"
    assert_output --partial "${TEMPLATE_NAME}"
}

@test "delete LimaVM resource" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}" --grace-period=0
}

@test "verify LimaVM is deleted" {
    run -1 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    assert_output --partial "not found"
}

@test "wait for template ConfigMap to be automatically deleted" {
    rdd ctl wait --for=delete configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --timeout="30s"
}

@test "create LimaVM with nonexistent template ConfigMap fails" {
    run -1 create_limavm "test-vm-missing" "nonexistent-template"
    assert_output --partial "not found"
}

@test "create LimaVM with ConfigMap missing template key fails" {
    rdd ctl create configmap "invalid-template" --namespace "${NAMESPACE}" --from-literal="foo=bar"

    run -1 create_limavm "test-vm-invalid" "invalid-template"
    # Mutating webhook tries to create template ConfigMap, ConfigMap webhook validates and rejects
    assert_output --partial '"template" data cannot be empty'
}

@test "create LimaVM with invalid YAML template fails" {
    rdd ctl create configmap "bad-yaml" --namespace "${NAMESPACE}" --from-literal='template=invalid: yaml: {'

    run -1 create_limavm "test-vm-bad-yaml" "bad-yaml"
    assert_output --partial "failed to parse template"
}

@test "create LimaVM with invalid Lima schema fails" {
    # arch parses fine as a string but fails Lima validation (not a valid architecture)
    rdd ctl create configmap "bad-lima" --namespace "${NAMESPACE}" --from-literal='template=arch: "cray-1"'

    run -1 create_limavm "test-vm-bad-lima" "bad-lima"
    assert_output --partial "failed to validate template"
    assert_output --partial "field \`arch\`"
    assert_output --partial 'got "cray-1"'
}

@test "updating LimaVM spec.running does not affect template ConfigMap" {
    create_limavm "test-vm-running" "source-template"

    # Wait for template ConfigMap to be created
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/test-vm-running" --namespace "${NAMESPACE}" --timeout="30s"

    # Update the running state
    run -0 rdd ctl patch limavm "test-vm-running" --namespace "${NAMESPACE}" --type='merge' --patch='{"spec":{"running":true}}'

    # Verify the template ConfigMap still exists and is unchanged
    run -0 rdd ctl get configmap test-vm-running-template --namespace "${NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output "${TEMPLATE}"
}
