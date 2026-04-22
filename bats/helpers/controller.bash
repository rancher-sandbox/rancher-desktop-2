# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Delete a resource. Does not return a failure if the resource doesn't exist.
# Will return a failure if the CRD for the resource_type is not established.
delete_resource() {
    local resource_type=$1
    local name=$2

    rdd ctl delete "${resource_type}" "${name}" -n "${RDD_NAMESPACE}" --grace-period=0 --ignore-not-found
}

# Assert that resource count matches expected value
assert_resource_count() {
    local resource_type=$1
    local controller_name=$2
    local name=$3
    local expected=$4

    run rdd ctl get "${resource_type}" -n "${RDD_NAMESPACE}" -l "app.kubernetes.io/managed-by=${controller_name},app.kubernetes.io/instance=${name}" -o json
    if [[ "${status}" -eq 0 ]]; then
        run -0 jq '.items | length' <<<"${output}"
    else
        output="0"
    fi
    assert_output "${expected}"
}

# Wait for resources to reach expected count
wait_for_resource_count() {
    local resource_type=$1
    local controller_name=$2
    local name=$3
    local expected=$4

    try --max 30 --delay 1 -- assert_resource_count "${resource_type}" "${controller_name}" "${name}" "${expected}"
}

get_resource_status() {
    local resource_type=$1
    local name=$2
    local field=$3

    rdd ctl get "${resource_type}" "${name}" -n "${RDD_NAMESPACE}" -o jsonpath="{.status.${field}}"
}

assert_resource_status() {
    local resource_type=$1
    local name=$2
    local field=$3
    local expected=$4

    run -0 get_resource_status "${resource_type}" "${name}" "${field}"
    assert_output "${expected}"
}

wait_for_resource_status() {
    local resource_type=$1
    local name=$2
    local field=$3
    local expected=$4

    try --max 30 --delay 1 -- assert_resource_status "${resource_type}" "${name}" "${field}" "${expected}"
}

patch_resource() {
    local resource_type=$1
    local name=$2
    local patch=$3

    rdd ctl patch "${resource_type}" "${name}" -n "${RDD_NAMESPACE}" --type='merge' -p="${patch}"
}

setup_rdd_control_plane() {
    local controllers=${1:-"*"}

    # Bound delete so a stuck prior daemon cannot hang the helper for the
    # full 5m stopWaitTimeout, and let the bounded delete fail loudly: a
    # timed-out delete leaves the instance directory behind, which turns
    # `create` into a no-op and leaks the prior controller set into the
    # next suite.
    rdd svc delete --timeout=120s
    rdd svc create --controllers="${controllers}"
    rdd svc start
}
