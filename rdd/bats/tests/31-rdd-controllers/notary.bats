load '../../helpers/load'

# Notary controller functional tests - tests core controller functionality
# including resource processing, ConfigMap creation, status updates, and events.
# For admission controller testing, see notary-admission.bats

NOTARY_CONTROLLER_NAME="notary-controller"

local_setup_file() {
    setup_rdd_control_plane "notary"
}

create_notary() {
    local name=$1
    local value=$2
    local config_map_name=$3

    rdd ctl apply -f - <<EOF || return 1
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: ${name}
  namespace: "${RDD_NAMESPACE}"
spec:
  value: "${value}"
  configMapName: "${config_map_name}"
EOF
}

update_notary_value() {
    local name=$1
    local value=$2
    patch_resource "notary" "${name}" "{\"spec\":{\"value\":\"${value}\"}}"
}

wait_for_notary_status() {
    local name=$1
    local expected=$2
    wait_for_resource_status "notary" "${name}" "lastRecordedValue" "${expected}"
}

@test 'create Notary resource' {
    rdd ctl create namespace "${RDD_NAMESPACE}" || true
    delete_resource "notary" "basic"
    create_notary "basic" "initial-value" "basic-history"
}

@test 'verify Notary is created in Kubernetes' {
    rdd ctl wait --for=create notary --namespace "${RDD_NAMESPACE}" basic --timeout=15s
}

@test 'wait for controller to create ConfigMap' {
    wait_for_resource_count "configmaps" "${NOTARY_CONTROLLER_NAME}" "basic" 1
}

@test 'verify ConfigMap has correct name and content' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" "basic-history" -o json
    local json="${output}"

    run -0 jq -r '.data.change_000' <<<"${json}"
    assert_output --partial "value=initial-value"

    run -0 jq -r '.data.latest_change' <<<"${json}"
    assert_output --partial "value=initial-value"

    run -0 jq -r '.data.change_count' <<<"${json}"
    assert_output 0
}

@test 'verify ConfigMap has correct labels and owner references' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" "basic-history" -o json
    local json="${output}"

    # Check labels
    run -0 jq -r '.metadata.labels."app.kubernetes.io/managed-by"' <<<"${json}"
    assert_output "notary-controller"

    run -0 jq -r '.metadata.labels."app.kubernetes.io/instance"' <<<"${json}"
    assert_output "basic"

    # Check owner references
    run -0 jq -r '.metadata.ownerReferences[0].kind' <<<"${json}"
    assert_output "Notary"

    run -0 jq -r '.metadata.ownerReferences[0].name' <<<"${json}"
    assert_output "basic"

    run -0 jq -r '.metadata.ownerReferences[0].controller' <<<"${json}"
    assert_output "true"

    run -0 jq -r '.metadata.ownerReferences[0].blockOwnerDeletion' <<<"${json}"
    assert_output "true"
}

@test 'verify Notary status is updated' {
    run -0 rdd ctl get notary --namespace "${RDD_NAMESPACE}" basic -o json
    local json="${output}"

    run -0 jq -r 'has("status")' <<<"${json}"
    assert_output "true"

    run -0 jq -r '.status.lastRecordedValue' <<<"${json}"
    assert_output "initial-value"

    run -0 jq -r '.status.configMapStatus' <<<"${json}"
    assert_output "Updated"

    run -0 jq -r '.status.changeCount' <<<"${json}"
    assert_output 1
}

@test 'update Notary value' {
    update_notary_value "basic" "updated-value"
}

@test 'wait for status to reflect updated value' {
    wait_for_notary_status "basic" "updated-value"
}

@test 'verify ConfigMap records the change' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" "basic-history" -o json
    local json="${output}"

    run -0 jq -r '.data.change_000' <<<"${json}"
    assert_output --partial "value=initial-value"

    run -0 jq -r '.data.change_001' <<<"${json}"
    assert_output --partial "value=updated-value"

    run -0 jq -r '.data.latest_change' <<<"${json}"
    assert_output --partial "value=updated-value"

    run -0 jq -r '.data.change_count' <<<"${json}"
    assert_output 1
}

@test 'verify Notary status shows updated change count' {
    run -0 rdd ctl get notary --namespace "${RDD_NAMESPACE}" basic -o json
    local json="${output}"

    run -0 jq -r '.status.lastRecordedValue' <<<"${json}"
    assert_output "updated-value"

    run -0 jq -r '.status.configMapStatus' <<<"${json}"
    assert_output "Updated"

    run -0 jq -r '.status.changeCount' <<<"${json}"
    assert_output 2
}

@test 'update Notary value multiple times' {
    update_notary_value "basic" "third-value"
    wait_for_notary_status "basic" "third-value"

    update_notary_value "basic" "fourth-value"
    wait_for_notary_status "basic" "fourth-value"
}

@test 'verify ConfigMap records multiple changes' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" "basic-history" -o json
    local json="${output}"

    # Verify all changes are recorded
    run -0 jq -r '.data.change_000' <<<"${json}"
    assert_output --partial "value=initial-value"

    run -0 jq -r '.data.change_001' <<<"${json}"
    assert_output --partial "value=updated-value"

    run -0 jq -r '.data.change_002' <<<"${json}"
    assert_output --partial "value=third-value"

    run -0 jq -r '.data.change_003' <<<"${json}"
    assert_output --partial "value=fourth-value"

    # Verify latest change and count
    run -0 jq -r '.data.latest_change' <<<"${json}"
    assert_output --partial "value=fourth-value"

    run -0 jq -r '.data.change_count' <<<"${json}"
    assert_output 3
}

@test 'test same value does not create duplicate entries' {
    # Update with the same value
    update_notary_value "basic" "fourth-value"

    # We don't have any way to check if reconciliation has been performed, so we just wait a little
    sleep 2

    # Check that no new change was recorded
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" "basic-history" -o json
    local json="${output}"

    run -0 jq -r '.data.change_003' <<<"${json}"
    assert_output --partial "value=fourth-value"

    run -0 jq -r '.data.change_count' <<<"${json}"
    assert_output 3

    # Should NOT have change_004
    run -0 jq -r '.data | has("change_004")' <<<"${json}"
    assert_output "false"
}

@test 'verify Notary has finalizer for cleanup' {
    run -0 rdd ctl get notary --namespace "${RDD_NAMESPACE}" basic -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test 'delete Notary resource' {
    delete_resource "notary" "basic"
}

@test 'verify parent resource deletion triggers finalizer cleanup' {
    run -1 rdd ctl get notary --namespace "${RDD_NAMESPACE}" basic
}

@test 'wait for ConfigMaps to be cleaned up by finalizer' {
    wait_for_resource_count "configmaps" "${NOTARY_CONTROLLER_NAME}" "basic" 0
}
