# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# LimaVM running tests verify that Lima instances can be started and stopped.

NAMESPACE="running-test-ns"
VM_NAME="alpine-running"

# Use a minimal Alpine template for faster boot
ALPINE_TEMPLATE='
images:
- location: https://github.com/lima-vm/alpine-lima/releases/download/v0.2.47/alpine-lima-std-3.23.0-x86_64.iso
  arch: x86_64
  digest: sha512:c71e21dfb152642dd79af281497f86e7f690998997f787307978d83594e5e47addbc61e7d8ee405b0afc4230688de9eeb98fa44d6e74654e8d9d8b70151fb8da
- location: https://github.com/lima-vm/alpine-lima/releases/download/v0.2.47/alpine-lima-std-3.23.0-aarch64.iso
  arch: aarch64
  digest: sha512:44659e71c1e277361bc10ecc813fba799d0371c2bc291db811e05e43429fd31aaa7ebe185331d02dccadccddfe9d54184376cdceeb1c444b58e3e9a4e690ce33
containerd:
  system: false
  user: false'

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "${NAMESPACE}"
}

local_teardown_file() {
    # Clean up any remaining Lima instances
    if [[ -d "${LIMA_HOME}/${VM_NAME}" ]]; then
        rm -rf "${LIMA_HOME:?}/${VM_NAME}"
    fi
}

local_setup() {
    skip_on_windows
}

create_limavm_running() {
    local name=$1
    local template_name=$2
    local running=$3

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
  running: ${running}
EOF
}

assert_instance_created_condition() {
    local name=$1
    local expected=$2
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceCreated")].status}'
    assert_output "${expected}"
}

assert_instance_running_condition() {
    local name=$1
    local expected=$2
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceRunning")].status}'
    assert_output "${expected}"
}

assert_instance_running_reason() {
    local name=$1
    local expected=$2
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceRunning")].reason}'
    assert_output "${expected}"
}

assert_stdout_logs_contain() {
    local name=$1
    local expected=$2
    run -0 rdd limavm logs --stdout "${name}"
    assert_output --partial "${expected}"
}

lima_instance_running() {
    local name=$1
    assert_file_exists "${LIMA_HOME}/${name}/ha.pid"
}

get_instance_running_transition_time() {
    local name=$1
    rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.conditions[?(@.type=="InstanceRunning")].lastTransitionTime}'
}

get_hostagent_pid() {
    local name=$1
    cat "${LIMA_HOME}/${name}/ha.pid"
}

assert_recovery_completed() {
    local before_time=$1
    run -0 get_instance_running_transition_time "${VM_NAME}"
    refute_output "${before_time}"
    assert_instance_running_reason "${VM_NAME}" "Started"
}

assert_limavm_not_exists() {
    local name=$1
    run ! rdd ctl get limavm "${name}" --namespace "${NAMESPACE}"
}

@test "create source template ConfigMap for running tests" {
    rdd ctl create configmap "alpine-source" --namespace "${NAMESPACE}" --from-literal="template=${ALPINE_TEMPLATE}"
    run -0 rdd ctl get configmap "alpine-source" --namespace "${NAMESPACE}" --output jsonpath='{.data.template}'
    assert_output --partial "alpine-lima"
}

@test "create LimaVM with running=false" {
    create_limavm_running "${VM_NAME}" "alpine-source" "false"
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for InstanceCreated condition" {
    try --max 120 --delay 1 -- assert_instance_created_condition "${VM_NAME}" "True"
}

@test "verify initial InstanceRunning condition is False" {
    # Wait a bit for the controller to set the initial condition
    try --max 30 --delay 1 -- assert_instance_running_condition "${VM_NAME}" "False"
}

@test "verify initial InstanceRunning reason is Stopped" {
    assert_instance_running_reason "${VM_NAME}" "Stopped"
}

@test "start the VM by setting running=true" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":true}}'

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.spec.running}'
    assert_output "true"
}

@test "wait for InstanceRunning condition to become True" {
    # VM boot can take several minutes
    try --max 60 --delay 5 -- assert_instance_running_condition "${VM_NAME}" "True"
}

@test "verify InstanceRunning reason is Started" {
    assert_instance_running_reason "${VM_NAME}" "Started"
}

@test "verify hostagent PID file exists" {
    lima_instance_running "${VM_NAME}"
}

@test "logs shows hostagent stderr" {
    run -0 rdd limavm logs "${VM_NAME}"
    assert_output --partial "hostagent socket created"
}

@test "logs --stdout shows hostagent stdout" {
    # The hostagent writes its first stdout event only after the VM's SSH port
    # becomes accessible, which can lag behind the InstanceRunning condition.
    try --max 60 --delay 5 -- assert_stdout_logs_contain "${VM_NAME}" "sshLocalPort"
}

@test "stop the VM by setting running=false" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":false}}'

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.spec.running}'
    assert_output "false"
}

@test "wait for InstanceRunning condition to become False" {
    # Graceful shutdown can take up to 3 minutes
    try --max 60 --delay 5 -- assert_instance_running_condition "${VM_NAME}" "False"
}

@test "verify InstanceRunning reason is Stopped after stop" {
    assert_instance_running_reason "${VM_NAME}" "Stopped"
}

@test "verify hostagent PID file is removed" {
    try --max 30 --delay 1 --until-fail -- lima_instance_running "${VM_NAME}"
}

# Crash recovery tests
# These tests verify that the controller recovers when the hostagent crashes.
# On VZ the VM dies with the hostagent (runs in-process) so Lima sees Stopped.
# On QEMU the VM outlives the hostagent so Lima sees Broken, then force-stops
# the orphaned QEMU process. Either way the controller restarts the full VM.

@test "start VM for crash recovery test" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":true}}'
    try --max 60 --delay 5 -- assert_instance_running_condition "${VM_NAME}" "True"
}

@test "simulate crash by killing hostagent" {
    local pid_file="${LIMA_HOME}/${VM_NAME}/ha.pid"
    assert_file_exists "${pid_file}"

    # shellcheck disable=SC2030 # Persisted via save_var, not subshell
    OLD_HA_PID=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${OLD_HA_PID}" ]
    # Verify the PID is a running process. On Windows the hostagent runs as a
    # Win32 process but BATS runs inside WSL, so kill can't reach it.
    kill -0 "${OLD_HA_PID}"
    save_var OLD_HA_PID

    kill -9 "${OLD_HA_PID}"
}

@test "trigger reconcile and verify crash recovery" {
    run -0 get_instance_running_transition_time "${VM_NAME}"
    assert_output
    before_time=${output}

    rdd ctl annotate limavm "${VM_NAME}" --namespace "${NAMESPACE}" --overwrite \
        reconcile-trigger="$(date +%s)"

    # The reconciler should detect the dead hostagent and restart the VM.
    try --max 60 --delay 5 -- assert_recovery_completed "${before_time}"
}

@test "verify new hostagent after crash recovery" {
    local pid_file="${LIMA_HOME}/${VM_NAME}/ha.pid"
    assert_file_exists "${pid_file}"
    local new_pid
    new_pid=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${new_pid}" ]
    kill -0 "${new_pid}"

    load_var OLD_HA_PID
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    refute [ "${new_pid}" = "${OLD_HA_PID}" ]
}

# Broken state recovery tests
# These tests verify that the controller can recover from a broken Lima instance.
# We simulate breakage by replacing the hostagent socket with a regular file.
# This makes Lima report StatusBroken (socket exists but is not a Unix socket)
# without killing the VM process (which on VZ runs inside the hostagent).

@test "simulate broken state by replacing hostagent socket" {
    local sock_file="${LIMA_HOME}/${VM_NAME}/ha.sock"
    assert_socket_exists "${sock_file}"

    # Replace the Unix socket with a regular file so Lima can't connect
    rm "${sock_file}"
    touch "${sock_file}"
    assert_file_exists "${sock_file}"
}

@test "trigger reconcile and verify recovery from broken state" {
    # Capture the lastTransitionTime before triggering reconcile
    run -0 get_instance_running_transition_time "${VM_NAME}"
    assert_output
    before_time=${output}

    # Annotate to trigger a reconcile while spec.running is still true
    rdd ctl annotate limavm "${VM_NAME}" --namespace "${NAMESPACE}" --overwrite \
        reconcile-trigger="$(date +%s)"

    # The reconciler should detect broken state, force stop, then restart.
    # Verify the condition was actually updated by checking lastTransitionTime changed.
    try --max 60 --delay 5 -- assert_recovery_completed "${before_time}"
}

@test "verify hostagent is alive after recovery" {
    local pid_file="${LIMA_HOME}/${VM_NAME}/ha.pid"
    assert_file_exists "${pid_file}"
    local pid
    pid=$(cat "${pid_file}")
    assert [ -n "${pid}" ]
    kill -0 "${pid}"
}

@test "verify BrokenStateRecovered event was emitted" {
    run -0 rdd ctl get events --namespace "${NAMESPACE}" \
        --field-selector involvedObject.kind=LimaVM,involvedObject.name="${VM_NAME}"
    assert_output --partial "BrokenStateRecovered"
}

@test "cleanup LimaVM running test" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    try --max 60 --delay 1 -- assert_limavm_not_exists "${VM_NAME}"
}
