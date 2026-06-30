# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Recovery from an orphaned WSL2 distro registration: a distro still registered
# with WSL but missing its ext4.vhdx root disk, the state a removed instance
# directory leaves behind. On start rdd must unregister it so Lima re-imports a
# fresh distro, instead of booting a missing disk into ERROR_FILE_NOT_FOUND
# forever. The orphan state only exists for WSL2, so every test skips off
# Windows.
#
# The test boots the real distro and then deletes its disk, which leaves the
# same instance state (WSL2, Stopped, disk-missing) the predicate keys on as a
# removed directory would, without depending on Lima re-creating the metadata.

NAMESPACE="wsl2-orphan-ns"
VM_NAME="test-orphan"
ORPHAN_TEMPLATE=$(vm_template)

local_setup() {
    is_windows || skip "WSL2 orphan recovery is Windows-only"
}

local_setup_file() {
    is_windows || return 0
    setup_rdd_control_plane "lima"
    # `rdd svc delete` leaves the WSL registration when it removes the instance
    # directory, so a distro can outlive a previous run.
    unregister_distro "${VM_NAME}"
    rm -rf "${RDD_LIMA_HOME:?}/${VM_NAME}"
    rdd ctl create namespace "${NAMESPACE}"
}

instance_disk() { # <name>
    echo "${RDD_LIMA_HOME}/$1/ext4.vhdx"
}

# wsl.exe --unregister can hang, so cap it with a timeout.
unregister_distro() { # <name>
    timeout 30 env MSYS_NO_PATHCONV=1 wsl.exe --unregister "lima-$1" 2>/dev/null || true
}

# wsl.exe --shutdown can hang, so cap it with a timeout.
shutdown_wsl() {
    timeout 30 wsl.exe --shutdown 2>/dev/null || true
}

# Asserts the instance's WSL2 distro is registered with WSL, running or stopped.
# WSL_UTF8 makes wsl.exe emit UTF-8 instead of its default UTF-16.
assert_distro_registered() { # <name>
    run -0 env MSYS_NO_PATHCONV=1 WSL_UTF8=1 wsl.exe --list --quiet
    assert_output --partial "lima-$1"
}

assert_distro_running() { # <name>
    run -0 env MSYS_NO_PATHCONV=1 WSL_UTF8=1 wsl.exe --list --running
    assert_output --partial "lima-$1"
}

@test "create the source template ConfigMap" {
    rdd ctl create configmap "source-template" --namespace "${NAMESPACE}" \
        --from-literal="template=${ORPHAN_TEMPLATE}"
}

@test "create and start the VM" {
    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: ${VM_NAME}
  namespace: ${NAMESPACE}
spec:
  templateRef:
    name: source-template
    namespace: ${NAMESPACE}
  running: true
EOF
    rdd ctl wait --for=condition=Running=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

@test "the imported distro created its ext4.vhdx root disk" {
    # Confirms wsl2RootDisk ("ext4.vhdx") matches the filename WSL's --import
    # creates -- the real-world assumption the predicate's unit test cannot
    # reach.
    assert_file_exists "$(instance_disk "${VM_NAME}")"
    assert_distro_running "${VM_NAME}"
}

@test "stop the VM, leaving its distro registered but not running" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":false}}'
    rdd ctl wait --for=condition=Running=False \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

@test "orphan the registration by deleting the root disk" {
    assert_distro_registered "${VM_NAME}"
    # The WSL2 utility VM keeps ext4.vhdx attached after the distro stops -- even
    # past the `wsl --terminate` that stop runs -- so only `wsl --shutdown` frees
    # the handle. --shutdown leaves the registration, so the orphan state holds.
    shutdown_wsl
    try --max 10 --delay 1 -- rm -f "$(instance_disk "${VM_NAME}")"
    assert_file_not_exists "$(instance_disk "${VM_NAME}")"
    # The registration outlives the disk -- the orphan state under test.
    assert_distro_registered "${VM_NAME}"
}

@test "start re-imports the distro and recovers" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":true}}'
    # Without the heal, Lima boots the missing disk into ERROR_FILE_NOT_FOUND
    # forever and this wait times out.
    rdd ctl wait --for=condition=Running=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
    assert_file_exists "$(instance_disk "${VM_NAME}")"
    assert_distro_running "${VM_NAME}"
}

@test "cleanup" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    # A failed wsl.exe --unregister can deadlock wslservice.exe and block the
    # reconciler's store.Inspect, so wrap the wait in an outer timeout. Kill
    # wslservice.exe to recover.
    if ! timeout 90 rdd ctl wait --for=delete \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=60s; then
        MSYS_NO_PATHCONV=1 taskkill.exe /F /IM wslservice.exe || true
        false
    fi
}
