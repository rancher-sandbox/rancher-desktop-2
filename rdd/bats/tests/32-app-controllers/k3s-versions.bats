# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

CONFIG_MAP_NAME="k3s-versions"
APP_NAME="app"

local_setup_file() {
    setup_rdd_control_plane
}

delete_app() {
    rdd ctl delete "app/${APP_NAME}" --ignore-not-found
    # Wait for full deletion so that create_app always starts with no
    # pre-existing App resource. Without this, rdd ctl apply in create_app
    # can update a still-terminating App, which the controller treats as a
    # deletion request — no LimaVM is ever created.
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=120s 2>/dev/null || true
}

create_app() {
    # The app is always created without a running VM.
    rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: ${APP_NAME}
spec:
  namespace: ${RDD_NAMESPACE}
  running: false
EOF
}

@test "config map should not exist before creating App" {
    run -1 rdd ctl get "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
}

@test "create App resource" {
    delete_app
    create_app
}

@test "config map should exist after creating App" {
    wait_for_resource_count "configmap" "rdd-k3s-versions" "${APP_NAME}" 1
    run -0 rdd ctl get --output=json "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
    assert_output
    channels=$(jq_output '.data.channels')
    versions=$(jq_output '.data.versions')
    run -0 jq .stable <<<"${channels}"
    assert_output
    run -0 jq ".versions[${output}]" <<<"${versions}"
    assert_output
}

@test "config map should not be deletable while App exists" {
    run -1 rdd ctl delete "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
    assert_output --partial "forbidden"
}

function assert_config_map_reconciled() {
    run -0 rdd ctl get --output=jsonpath='{.data.channels}' "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
    assert_output
    run -0 jq_output .stable
    assert_output
}

@test "config map updates should be reverted while App exists" {
    rdd ctl patch "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}" --type=merge --patch '{"data":{"channels":null}}'
    try assert_config_map_reconciled
}

@test "config map should accept extra keys" {
    rdd ctl patch "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}" --type=merge --patch '{"data":{"hello":"world"}}'
    rdd ctl patch "app/${APP_NAME}" --type=merge --patch '{"spec":{"kubernetes":{"enabled":true}}}'
    run -0 rdd ctl get --output=json "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
    assert_output
    data=${output}
    channels=$(jq_raw '.data.channels' "${data}")
    run -0 jq .stable <<<"${channels}"
    assert_output
    jq_raw '.data.hello' "${data}"
    assert_output world
}

@test "config map should be deleted after deleting App" {
    delete_app
    run -1 rdd ctl get "--namespace=${RDD_NAMESPACE}" "configmap/${CONFIG_MAP_NAME}"
    assert_output --partial "not found"
}
