# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# App controller lifecycle bats tests controller's singleton creation, LimaVM ownership,
# running propagation, condition mirroring, and deletion.

APP_NAME="app"
VM_NAME="rd"
INPUT_CM_NAME="rd"

delete_app() {
    rdd ctl delete app "${APP_NAME}" --ignore-not-found
    # Wait for full deletion so that create_app always starts with no
    # pre-existing App resource. Without this, rdd ctl apply in create_app
    # can update a still-terminating App, which the controller treats as a
    # deletion request — no LimaVM is ever created.
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=120s 2>/dev/null || true
}

create_app() {
    local running=${1:-false}
    delete_app

    rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: ${APP_NAME}
spec:
  namespace: ${RDD_NAMESPACE}
  running: ${running}
EOF
}

local_setup_file() {
    setup_rdd_control_plane "app,limavm"
}

@test "create App resource" {
    create_app false
}

@test "verify App is cluster-scoped and accessible without a namespace" {
    run -0 rdd ctl get app "${APP_NAME}"
}

@test "verify App has cleanup finalizer" {
    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test "wait for LimaVM to be created" {
    rdd ctl wait --for=create limavm/"${VM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "verify App and LimaVM are in the 'all' category" {
    run -0 rdd ctl api-resources --categories=all --output=name
    assert_line apps.app.rancherdesktop.io
    assert_line limavms.lima.rancherdesktop.io
}

@test "verify 'get all' returns the LimaVM instance" {
    run -0 rdd ctl get all --namespace "${RDD_NAMESPACE}" --output=name
    assert_line "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "verify LimaVM is named rd" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "verify LimaVM has owned finalizer set by App controller" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/owned-by-App"
}

@test "verify LimaVM has cleanup finalizer set by LimaVM webhook" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test "verify LimaVM has owner reference pointing to App" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    local json="${output}"

    run -0 jq -r '.metadata.ownerReferences[0].kind' <<<"${json}"
    assert_output "App"

    run -0 jq -r '.metadata.ownerReferences[0].name' <<<"${json}"
    assert_output "${APP_NAME}"

    run -0 jq -r '.metadata.ownerReferences[0].controller' <<<"${json}"
    assert_output "true"
}

@test "verify LimaVM templateRef points to input ConfigMap" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.templateRef.name}'
    assert_output "${INPUT_CM_NAME}"
}

@test "verify LimaVM spec.running starts as false" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.running}'
    assert_output "false"
}

@test "wait for LimaVM webhook to copy template into rd-template ConfigMap" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}'="${VM_NAME}-template" \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "verify input ConfigMap is deleted after LimaVM copies it" {
    rdd ctl wait --for=delete configmap/"${INPUT_CM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "verify rd-template ConfigMap exists and contains template data" {
    run -0 rdd ctl get configmap "${VM_NAME}-template" \
        --namespace "${RDD_NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output
}

@test "wait for LimaVM Created condition to be set" {
    rdd ctl wait --for=condition=Created \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "verify App status mirrors LimaVM Created condition" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    local json="${output}"

    run -0 jq -r '.status | has("conditions")' <<<"${json}"
    assert_output "true"

    run -0 jq -r '.status.conditions[] | select(.type == "Created") | .status' <<<"${json}"
    assert_output "True"
}

@test "verify App conditions preserve LimaVM LastTransitionTime" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Created") | .lastTransitionTime'
    local limavm_ts="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Created") | .lastTransitionTime'
    assert_output "${limavm_ts}"
}

@test "propagating running=true updates LimaVM spec.running" {
    rdd ctl patch app "${APP_NAME}" --type='merge' -p='{"spec":{"running":true}}'
    rdd ctl wait --for=jsonpath='{.spec.running}'=true \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "wait for LimaVM Running condition to become true after start" {
    rdd ctl wait --for=condition=Running \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=120s
}

@test "wait for App Running condition to become True" {
    rdd ctl wait --for=condition=Running app/"${APP_NAME}" --timeout=30s
}

@test "verify App mirrors LimaVM Running condition as True" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .status'
    assert_output "True"
}

@test "verify App Running condition preserves LimaVM LastTransitionTime" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .lastTransitionTime'
    local limavm_ts="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .lastTransitionTime'
    assert_output "${limavm_ts}"
}

@test "verify App Running condition observedGeneration reflects App generation" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.metadata.generation'
    local app_gen="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .observedGeneration'
    assert_output "${app_gen}"
}

@test "propagating running=false updates LimaVM spec.running" {
    rdd ctl patch app "${APP_NAME}" --type='merge' -p='{"spec":{"running":false}}'
    rdd ctl wait --for=jsonpath='{.spec.running}'=false \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "wait for LimaVM Running condition to become false after stop" {
    rdd ctl wait --for=condition=Running=False \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "verify App mirrors LimaVM Running condition as False after stop" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .status'
    assert_output "False"
}

@test "reject App resource with a name other than 'app'" {
    run -1 rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: not-app
spec:
  namespace: ${RDD_NAMESPACE}
  running: false
EOF
    assert_output --partial "App resource must be named 'app'"
}

@test "reject mutation of spec.namespace after creation" {
    run -1 rdd ctl patch app "${APP_NAME}" --type='merge' \
        -p='{"spec":{"namespace":"other-ns"}}'
    assert_output --partial "spec.namespace is immutable"
}

@test "reject direct deletion of LimaVM while owned by App" {
    run -1 rdd ctl delete limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" --grace-period=0
    assert_output --partial "Forbidden"
    assert_output --partial "owned by App"
    assert_output --partial "delete the App resource instead"

    # Verify the LimaVM still exists and is not stuck in Terminating
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}"
}

@test "delete App resource" {
    delete_app
}

@test "wait for App to be fully deleted" {
    # The App finalizer stays until the LimaVM controller finishes teardown.
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=90s
}

@test "verify LimaVM rd is deleted when App is deleted" {
    run -1 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}"
    assert_output --partial "not found"
}

@test "verify App can be recreated after deletion" {
    create_app false
    rdd ctl wait --for=create limavm/"${VM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=60s
    delete_app
}
