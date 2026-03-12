# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# LimaVM instance tests verify that Lima instances are created on disk
# after template verification, and deleted when the LimaVM is deleted.

NAMESPACE="instance-test-ns"
VM_NAME="opensuse-test"

# Minimal template with openSUSE qcow2 images from rancher-desktop-opensuse.
# vmType defaults to vz on macOS and qemu on Linux.
OPENSUSE_TEMPLATE='
images:
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.1.1/distro.v0.1.1.amd64.qcow2
  arch: x86_64
  digest: sha256:6a0a2729781f7a412f2d4fd7cb3270104eb16d9965811d0a39cb9766afdf3fd3
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.1.1/distro.v0.1.1.arm64.qcow2
  arch: aarch64
  digest: sha256:8e8f9dfa8292dd4e3821f44542305b01c78ec8cb007065d1bba233899ce438e8
containerd:
  system: false
  user: false'

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
}

local_setup() {
    skip_on_windows
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

@test "create source template ConfigMap with openSUSE image" {
    rdd ctl create configmap "opensuse-source" --namespace "${NAMESPACE}" --from-literal="template=${OPENSUSE_TEMPLATE}"

    run -0 rdd ctl get configmap "opensuse-source" --namespace "${NAMESPACE}" --output jsonpath='{.data.template}'
    assert_output --partial "rancher-desktop-opensuse"
}

@test "create LimaVM with openSUSE template" {
    create_limavm "${VM_NAME}" "opensuse-source"

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for template ConfigMap to be created" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout="30s"
}

@test "wait for Created condition to be True" {
    rdd ctl wait --for=condition=Created=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=60s
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
    create_limavm "${VM_NAME}" "opensuse-source"
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for Created after leftover cleanup" {
    rdd ctl wait --for=condition=Created=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=60s
}

@test "verify leftover was replaced with real instance" {
    # The fake leftover had a .fake-leftover sentinel file
    assert_file_not_exists "${RDD_LIMA_HOME}/${VM_NAME}/.fake-leftover"
    # Real instance should have images from template
    run -0 cat "${RDD_LIMA_HOME}/${VM_NAME}/lima.yaml"
    assert_output --partial "rancher-desktop-opensuse"
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
