# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# LimaVM running tests verify that Lima instances can be started and stopped.

NAMESPACE="running-test-ns"
VM_NAME="test-running"
TEMPLATE_NAME="${VM_NAME}-template"

# vm_template supports RDD_VM_TYPE on macOS/Linux (e.g. RDD_VM_TYPE=qemu).
RUNNING_TEMPLATE=$(vm_template)

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "${NAMESPACE}"
}

local_teardown_file() {
    # Clean up any remaining Lima instances
    if [[ -d "${RDD_LIMA_HOME}/${VM_NAME}" ]]; then
        rm -rf "${RDD_LIMA_HOME:?}/${VM_NAME}"
    fi
    # Clean up any WSL2 distros created by tests.
    # Use a timeout because wsl.exe --unregister can hang.
    if is_windows; then
        for vm in "${VM_NAME}" "stopped-vm"; do
            timeout 30 bash -c "MSYS_NO_PATHCONV=1 wsl.exe --unregister 'lima-${vm}'" 2>/dev/null || true
        done
    fi
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

assert_stdout_logs_contain() {
    local name=$1
    local expected=$2
    run -0 rdd limavm logs --stdout "${name}"
    assert_output --partial "${expected}"
}

lima_instance_running() {
    local name=$1
    assert_file_exists "${RDD_LIMA_HOME}/${name}/ha.pid"
}

get_restart_count() {
    local name=$1
    rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.restartCount}'
}

get_hostagent_pid() {
    local name=$1
    cat "${RDD_LIMA_HOME}/${name}/ha.pid"
}

assert_shell_succeeds() {
    local name=$1
    shift
    rdd lima shell "${name}" "$@"
}

# On Windows, rdd.exe can't execute MSYS2 paths directly. Use "bash <path>"
# so the editor command is resolved by bash, and convert the path for Win32.
editor_cmd() {
    local script=$1
    if is_msys; then
        echo "bash '$(cygpath -m "${script}")'"
    else
        echo "${script}"
    fi
}

@test "create source template ConfigMap for running tests" {
    rdd ctl create configmap "source-template" --namespace "${NAMESPACE}" --from-literal="template=${RUNNING_TEMPLATE}"
    run -0 rdd ctl get configmap "source-template" --namespace "${NAMESPACE}" --output jsonpath='{.data.template}'
    assert_output --partial "images:"
}

@test "create LimaVM with running=false" {
    create_limavm_running "${VM_NAME}" "source-template" "false"
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" --output name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "wait for Created condition" {
    rdd ctl wait --for=condition=Created \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=120s
}

@test "verify initial Running condition is False/Stopped" {
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running=False --reason=Stopped --timeout=120s
}

@test "start the VM by setting running=true" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":true}}'

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.spec.running}'
    assert_output "true"
}

@test "wait for Running condition to become True/Started" {
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running --reason=Started --timeout=300s
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
    # becomes accessible, which can lag behind the Running condition.
    try --max 60 --delay 5 -- assert_stdout_logs_contain "${VM_NAME}" "sshLocalPort"
}

@test "shell executes command in running VM" {
    # SSH inside the guest may not be ready immediately after Running=True
    try --max 12 --delay 5 -- assert_shell_succeeds "${VM_NAME}" uname -s
    assert_line "Linux"
}

@test "shell fails when VM is not running" {
    # Create a stopped VM to test shell error
    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: stopped-vm
  namespace: ${NAMESPACE}
spec:
  templateRef:
    name: source-template
    namespace: ${NAMESPACE}
  running: false
EOF
    rdd ctl wait --for=condition=Created \
        "limavm/stopped-vm" --namespace "${NAMESPACE}" --timeout=120s

    run -1 rdd lima shell "stopped-vm"
    assert_output --partial "is not running"

    # Cleanup
    rdd ctl delete limavm "stopped-vm" --namespace "${NAMESPACE}" --grace-period=0
}

@test "stop the VM by setting running=false" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":false}}'

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.spec.running}'
    assert_output "false"
}

@test "wait for Running condition to become False/Stopped" {
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running=False --reason=Stopped --timeout=300s
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
    rdd ctl wait --for=condition=Running=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

@test "simulate crash by killing hostagent" {
    local pid_file="${RDD_LIMA_HOME}/${VM_NAME}/ha.pid"
    assert_file_exists "${pid_file}"

    # shellcheck disable=SC2030 # Persisted via save_var, not subshell
    OLD_HA_PID=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${OLD_HA_PID}" ]
    # Verify the PID is a running process.
    assert_process_alive "${OLD_HA_PID}"
    save_var OLD_HA_PID

    # Save restart count before the kill. The watcher detects the crash
    # instantly, so recovery may complete before the next test starts.
    # shellcheck disable=SC2030 # Persisted via save_var, not subshell
    BEFORE_KILL_COUNT=$(get_restart_count "${VM_NAME}")
    save_var BEFORE_KILL_COUNT

    process_kill "${OLD_HA_PID}"
}

@test "trigger reconcile and verify crash recovery" {
    load_var BEFORE_KILL_COUNT

    # Annotate the resource to exercise concurrent modification during recovery.
    # The watcher already triggers a reconcile, but users may also modify the
    # object while the controller is restarting the hostagent.
    rdd ctl annotate limavm "${VM_NAME}" --namespace "${NAMESPACE}" --overwrite \
        reconcile-trigger="$(date +%s)"

    # restartCount increments in the same status write that sets Running=True.
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    rdd ctl wait --for=jsonpath='{.status.restartCount}'="$((BEFORE_KILL_COUNT + 1))" \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

@test "verify new hostagent after crash recovery" {
    local pid_file="${RDD_LIMA_HOME}/${VM_NAME}/ha.pid"
    assert_file_exists "${pid_file}"
    local new_pid
    new_pid=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${new_pid}" ]
    assert_process_alive "${new_pid}"

    # Windows recycles PIDs quickly, so this assertion is only reliable on Unix.
    load_var OLD_HA_PID
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    if is_unix; then
        refute [ "${new_pid}" = "${OLD_HA_PID}" ]
    fi
}

# Orphaned hostagent recovery tests
# When the service crashes (SIGKILL), the hostagent survives (own process group).
# On restart, the controller detects the orphaned hostagent via store.Inspect(),
# kills it, and starts a fresh one with a watcher.
#
# Not tested: whether the orphaned hostagent shuts down gracefully (via
# CTRL_BREAK/SIGINT) or is force-killed. The test verifies the outcome
# (recovery succeeds) but does not distinguish the shutdown mechanism.

@test "save hostagent PID before service crash" {
    # shellcheck disable=SC2030 # Persisted via save_var, not subshell
    ORPHAN_HA_PID=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${ORPHAN_HA_PID}" ]
    assert_process_alive "${ORPHAN_HA_PID}"
    save_var ORPHAN_HA_PID

    # Save restart count to detect when recovery completes after restart.
    # shellcheck disable=SC2030 # Persisted via save_var, not subshell
    BEFORE_CRASH_COUNT=$(get_restart_count "${VM_NAME}")
    save_var BEFORE_CRASH_COUNT
}

@test "simulate service crash with SIGKILL" {
    local svc_pid
    svc_pid=$(cat "${RDD_PID_FILE}")
    assert [ -n "${svc_pid}" ]

    process_kill "${svc_pid}"

    # Wait for the service process to be fully reaped
    try --max 10 --delay 1 --until-fail -- assert_process_alive "${svc_pid}"
}

@test "verify hostagent survived service crash" {
    load_var ORPHAN_HA_PID
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    assert_process_alive "${ORPHAN_HA_PID}"
}

@test "restart service and verify orphaned hostagent recovery" {
    rdd svc start

    load_var ORPHAN_HA_PID
    load_var BEFORE_CRASH_COUNT

    # Wait for the controller to detect and kill the orphaned hostagent.
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    try --max 30 --delay 1 --until-fail -- assert_process_alive "${ORPHAN_HA_PID}"

    # restartCount increments when the new hostagent reaches Running.
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    rdd ctl wait --for=jsonpath='{.status.restartCount}'="$((BEFORE_CRASH_COUNT + 1))" \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s

    local new_pid
    new_pid=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${new_pid}" ]
    assert_process_alive "${new_pid}"

    # Windows recycles PIDs quickly, so this assertion is only reliable on Unix.
    # shellcheck disable=SC2031 # Loaded via load_var, not subshell
    if is_unix; then
        refute [ "${new_pid}" = "${ORPHAN_HA_PID}" ]
    fi
}

# Graceful shutdown test
# Verifies that stopping the service terminates all running hostagents.
# The controller's shutdown runnable calls shutdownAllHostagents() on exit.

@test "graceful service stop terminates hostagent" {
    local ha_pid
    ha_pid=$(get_hostagent_pid "${VM_NAME}")
    assert [ -n "${ha_pid}" ]
    assert_process_alive "${ha_pid}"

    rdd svc stop

    # On Windows, child process handles (wsl.exe) keep the hostagent PID visible
    # in the process table long after termination. The next test ("restart service
    # after graceful shutdown") verifies the hostagent is functionally dead.
    if is_unix; then
        try --max 15 --delay 1 --until-fail -- assert_process_alive "${ha_pid}"
    fi
}

@test "restart service after graceful shutdown" {
    rdd svc start

    # shutdownAllHostagents runs during graceful shutdown, so the hostagent
    # is already dead by the time the service exits.
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running --reason=Started --since=startup --timeout=300s
}

# Restart tests
# These tests verify the restart mechanism: annotation → status.restartNeeded → stop → start.

@test "restart running VM via rdd limavm restart" {
    run -0 get_restart_count "${VM_NAME}"
    local before_count=${output}

    # restart blocks until restartCount increments (Running=True/Started).
    rdd limavm restart "${VM_NAME}"

    run -0 get_restart_count "${VM_NAME}"
    assert_output "$((before_count + 1))"
}

@test "verify restartNeeded cleared and annotation removed after restart" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.restartNeeded}'
    refute_output # restartNeeded has omitempty; false means absent

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output "jsonpath={.metadata.annotations['lima\.rancherdesktop\.io/restartRequested']}"
    refute_output
}

@test "stop VM for stopped-restart test" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":false}}'
    rdd ctl wait --for=condition=Running=False \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

assert_restart_annotation_absent() {
    local name=$1
    run -0 rdd ctl get limavm "${name}" --namespace "${NAMESPACE}" \
        --output "jsonpath={.metadata.annotations['lima\.rancherdesktop\.io/restartRequested']}"
    refute_output
}

@test "restart annotation on stopped VM with running=false clears restartNeeded" {
    # Set annotation without changing spec.running (simulates annotation-only path)
    rdd ctl annotate limavm "${VM_NAME}" --namespace "${NAMESPACE}" --overwrite \
        "lima.rancherdesktop.io/restartRequested=$(date +%s)"

    # Wait for the annotation to be removed (reconciler processed it)
    try --max 30 --delay 1 -- assert_restart_annotation_absent "${VM_NAME}"

    # restartNeeded should be cleared
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.restartNeeded}'
    refute_output # restartNeeded has omitempty; false means absent

    # VM should stay stopped because spec.running=false
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running=False --reason=Stopped --timeout=30s
}

@test "restart command on stopped VM starts it" {
    # restart blocks until the VM is running again.
    rdd limavm restart "${VM_NAME}"

    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.status.restartNeeded}'
    refute_output # restartNeeded has omitempty; false means absent
}

# Template change detection tests
# These tests verify that the controller detects template ConfigMap changes,
# updates the on-disk lima.yaml, and restarts the instance if it was running.
# We use Lima's env: field because it writes to /etc/environment on every boot,
# so we can verify the new template took effect inside the guest.

# Append an env variable to the original template.
# Keep images identical so Lima doesn't re-download them.
MODIFIED_TEMPLATE="${RUNNING_TEMPLATE}
env:
  TEMPLATE_CHANGE_MARKER: applied"

assert_instance_template_contains() {
    local name=$1
    local expected=$2
    run -0 cat "${RDD_LIMA_HOME}/${name}/lima.yaml"
    assert_output --partial "${expected}"
}

assert_guest_env_var() {
    local name=$1
    local var=$2
    local expected=$3
    # Use --separate-stderr to avoid Lima driver warnings leaking into output.
    run --separate-stderr -0 rdd lima shell "${name}" printenv "${var}"
    assert_output "${expected}"
}

@test "verify on-disk template does not contain TEMPLATE_CHANGE_MARKER" {
    run -0 cat "${RDD_LIMA_HOME}/${VM_NAME}/lima.yaml"
    refute_output --partial "TEMPLATE_CHANGE_MARKER"
}

@test "patch template ConfigMap to trigger restart of running instance" {
    rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='merge' \
        --patch "$(jq -n --arg t "${MODIFIED_TEMPLATE}" '{"data":{"template":$t}}')"

    # Capture the patched ConfigMap's resourceVersion so we can wait for
    # the controller to observe it.
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.metadata.resourceVersion}'
    # shellcheck disable=SC2030
    PATCHED_TEMPLATE_RV="${output}"
    save_var PATCHED_TEMPLATE_RV
}

@test "wait for instance restart after template change" {
    load_var PATCHED_TEMPLATE_RV
    # The controller defers the observedTemplateResourceVersion update until
    # after the restart completes, so this wait implies the full restart cycle.
    # shellcheck disable=SC2031
    rdd ctl wait --for=jsonpath='{.status.observedTemplateResourceVersion}'="${PATCHED_TEMPLATE_RV}" \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=120s
    # Wait for the VM to finish restarting.
    rdd ctl wait --for=condition=Running=True \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

@test "verify on-disk template contains env variable after restart" {
    assert_instance_template_contains "${VM_NAME}" "TEMPLATE_CHANGE_MARKER"
}

@test "verify guest applied template change" {
    # SSH may not be ready immediately after Running=True
    try --max 12 --delay 5 -- assert_guest_env_var "${VM_NAME}" "TEMPLATE_CHANGE_MARKER" "applied"
}

@test "stop VM for stopped-template-change test" {
    rdd ctl patch limavm "${VM_NAME}" --namespace "${NAMESPACE}" \
        --type=merge --patch '{"spec":{"running":false}}'
    rdd ctl wait --for=condition=Running=False \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=300s
}

# Build a second modified template with a different env value.
MODIFIED_TEMPLATE_2="${RUNNING_TEMPLATE}
env:
  TEMPLATE_CHANGE_MARKER: updated-while-stopped"

@test "patch template ConfigMap while instance is stopped" {
    rdd ctl patch configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" --type='merge' \
        --patch "$(jq -n --arg t "${MODIFIED_TEMPLATE_2}" '{"data":{"template":$t}}')"

    # Capture the patched ConfigMap's resourceVersion.
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        --output jsonpath='{.metadata.resourceVersion}'
    # shellcheck disable=SC2030
    PATCHED_TEMPLATE_RV_2="${output}"
    save_var PATCHED_TEMPLATE_RV_2
}

@test "verify on-disk template updated without starting the instance" {
    load_var PATCHED_TEMPLATE_RV_2
    # shellcheck disable=SC2031
    rdd ctl wait --for=jsonpath='{.status.observedTemplateResourceVersion}'="${PATCHED_TEMPLATE_RV_2}" \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=30s
    assert_instance_template_contains "${VM_NAME}" "updated-while-stopped"
    rdd ctl await "limavm/${VM_NAME}" --namespace "${NAMESPACE}" \
        --for=condition=Running=False --reason=Stopped --timeout=30s
}

@test "cleanup LimaVM running test" {
    rdd ctl delete limavm "${VM_NAME}" --namespace "${NAMESPACE}"
    # Wrap with an outer timeout because rdd ctl wait --timeout may not be
    # enforced when the reconciler is stalled. On WSL2, a failed
    # wsl.exe --unregister can deadlock wslservice.exe, blocking all
    # subsequent wsl.exe calls (including store.Inspect in the reconciler).
    # If the wait hangs, kill wslservice.exe to recover.
    if ! timeout 90 rdd ctl wait --for=delete "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout=60s; then
        if is_windows; then
            MSYS_NO_PATHCONV=1 taskkill.exe /F /IM wslservice.exe || true
        fi
        false
    fi
}

@test "limavm edit command updates template ConfigMap" {
    create_limavm_running "${VM_NAME}" "source-template" "false"

    # Wait for template ConfigMap to be created
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}' \
        "limavm/${VM_NAME}" --namespace "${NAMESPACE}" --timeout="30s"

    # Since we can't interact with the editor in tests, we'll use a fake editor script that modifies the template file directly.
    # Set EDITOR to a script that modifies the file
    local editor_script="${BATS_TEST_TMPDIR}/fake-editor.sh"
    cat >"${editor_script}" <<'SCRIPT'
#!/bin/bash
# Replace the template content
echo 'images: [{"location":"https://bar.test."}]' > "$1"
SCRIPT

    chmod +x "${editor_script}"

    # Run edit with fake editor
    EDITOR="$(editor_cmd "${editor_script}")" run -0 rdd limavm edit "${VM_NAME}"
    assert_output --partial "updated successfully"

    # Verify ConfigMap was updated
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        -o jsonpath='{.data.template}'
    assert_output "images: [{\"location\":\"https://bar.test.\"}]"
}

@test "limavm edit aborts when all content is deleted" {
    # Capture current template
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        -o jsonpath='{.data.template}'

    local original_template="${output}"

    # Deleting editor
    local editor_script="${BATS_TEST_TMPDIR}/delete-editor.sh"
    cat >"${editor_script}" <<'SCRIPT'
#!/bin/bash
# Delete all content
echo "" > "$1"
SCRIPT

    chmod +x "${editor_script}"

    # Run edit aborts
    EDITOR="$(editor_cmd "${editor_script}")" run -1 rdd limavm edit "${VM_NAME}"
    assert_output --partial "template data was cleared, aborting edit"

    # Verify ConfigMap unchanged
    run -0 rdd ctl get configmap "${TEMPLATE_NAME}" --namespace "${NAMESPACE}" \
        -o jsonpath='{.data.template}'
    assert_output "${original_template}"
}

@test "limavm edit skips update when no changes made" {
    # No-op editor
    local editor_script="${BATS_TEST_TMPDIR}/noop-editor.sh"
    cat >"${editor_script}" <<'SCRIPT'
#!/bin/bash
# Do nothing and exit happy :)
exit 0
SCRIPT

    chmod +x "${editor_script}"

    EDITOR="$(editor_cmd "${editor_script}")" run -0 rdd limavm edit "${VM_NAME}"
    assert_output --partial "No changes made to template, skipping update"
}

@test "limavm edit fails gracefully for nonexistent VM" {
    run -1 rdd limavm edit "nonexistent-vm"
    assert_output --partial 'LimaVM \"nonexistent-vm\" not found in any namespace'
}

@test "limavm edit rejects invalid template" {
    skip
    # TODO: Implement test for invalid template edit once validation is wired in.
}
