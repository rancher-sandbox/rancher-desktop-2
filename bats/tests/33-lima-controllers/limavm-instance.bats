# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# LimaVM instance tests verify that Lima instances are created on disk
# after template verification, and deleted when the LimaVM is deleted.

NAMESPACE="instance-test-ns"
VM_NAME="test-instance"

INSTANCE_TEMPLATE=$(vm_template)

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "${NAMESPACE}"
}

local_teardown_file() {
    # Clean up any remaining Lima instances
    for vm in "${VM_NAME}" "invalid-vm"; do
        if [[ -d "${RDD_LIMA_HOME}/${vm}" ]]; then
            rm -rf "${RDD_LIMA_HOME:?}/${vm}"
        fi
    done
    # Clean up any WSL2 distros created by tests.
    # Use a timeout because wsl.exe --unregister can hang.
    if is_windows; then
        for vm in "${VM_NAME}" "invalid-vm"; do
            timeout 30 bash -c "MSYS_NO_PATHCONV=1 wsl.exe --unregister 'lima-${vm}'" 2>/dev/null || true
        done
    fi
}

create_limavm() {
    local name=$1
    local template_name=$2

    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: ${name}
  namespace: ${NAMESPACE}
spec:
  templateRef:
    name: ${template_name}
    namespace: ${NAMESPACE}
  running: false
EOF
}

lima_instance_exists() {
    local name=$1
    [[ -d "${RDD_LIMA_HOME}/${name}" ]]
}

@test "create source template ConfigMap" {
    rdd ctl create configmap "source-template" --namespace "${NAMESPACE}" --from-literal="template=${INSTANCE_TEMPLATE}"

    run -0 rdd ctl get configmap "source-template" --namespace "${NAMESPACE}" --output jsonpath='{.data.template}'
    assert_output --partial "images:"
}

@test "create LimaVM with source template" {
    create_limavm "${VM_NAME}" "source-template"

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for template ConfigMap to be created" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout="30s"
}

@test "wait for Created condition to be True" {
    rdd ctl wait --for=condition=Created=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=120s
}

@test "verify Lima instance directory exists" {
    try --max 30 --delay 1 -- lima_instance_exists "${VM_NAME}"
}

@test "verify Lima instance has lima.yaml file" {
    assert_file_exists "${RDD_LIMA_HOME}/${VM_NAME}/lima.yaml"
}

@test "verify Created condition has correct reason" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="Created")].reason}'
    assert_output "Created"
}

@test "verify Created condition has message" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="Created")].message}'
    # Message is "Lima instance created successfully" or "Lima instance exists"
    assert_output --partial "Lima instance"
}

@test "delete LimaVM resource" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}"
}

@test "verify LimaVM is deleted" {
    run -1 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    assert_output --partial "not found"
}

@test "verify Lima instance is deleted from disk" {
    try --max 30 --delay 1 --until-fail -- lima_instance_exists "${VM_NAME}"
}

# Test that leftover instances from failed deletions are cleaned up

@test "create fake leftover Lima instance" {
    # Create a fake instance directory to simulate a leftover from a failed deletion.
    # The reconciler should clean this up before creating the real instance.
    echo -n | create_file "${RDD_LIMA_HOME}/${VM_NAME}/lima.yaml"
    echo -n | create_file "${RDD_LIMA_HOME}/${VM_NAME}/.fake-leftover"
    echo "0.0.0" | create_file "${RDD_LIMA_HOME}/${VM_NAME}/lima-version"
    assert_file_exists "${RDD_LIMA_HOME}/${VM_NAME}/.fake-leftover"
}

@test "create LimaVM with leftover instance present" {
    create_limavm "${VM_NAME}" "source-template"
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for Created after leftover cleanup" {
    rdd ctl wait --for=condition=Created=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=120s
}

@test "verify leftover was replaced with real instance" {
    # The fake leftover had a .fake-leftover sentinel file
    assert_file_not_exists "${RDD_LIMA_HOME}/${VM_NAME}/.fake-leftover"
    # Real instance should have images from template
    run -0 cat "${RDD_LIMA_HOME}/${VM_NAME}/lima.yaml"
    assert_output --partial "images:"
}

@test "cleanup LimaVM after leftover test" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    try --max 30 --delay 1 --until-fail -- lima_instance_exists "${VM_NAME}"
}

# Test that invalid image URL causes Created condition to be False

INVALID_TEMPLATE='images:
- location: https://invalid.example.test/nonexistent.iso
  arch: x86_64
- location: https://invalid.example.test/nonexistent.iso
  arch: aarch64'

@test "create ConfigMap with invalid image URL" {
    rdd ctl create configmap "invalid-image" --namespace "${NAMESPACE}" --from-literal="template=${INVALID_TEMPLATE}"
}

@test "create LimaVM with invalid image URL" {
    create_limavm "invalid-vm" "invalid-image"
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/invalid-vm"
}

@test "wait for Created condition to be False" {
    rdd ctl wait --for=condition=Created=False \
        "limavm/invalid-vm" --namespace "${NAMESPACE}" --timeout=30s
}

@test "verify Created condition has CreateFailed reason" {
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="Created")].reason}'
    assert_output "CreateFailed"
}

@test "verify Created condition message contains error details" {
    run -0 rdd ctl get limavm "invalid-vm" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="Created")].message}'
    assert_output --partial "invalid.example.test"
}

@test "cleanup LimaVM with invalid image" {
    rdd ctl delete limavm "invalid-vm" --namespace "${NAMESPACE}" --ignore-not-found
}
