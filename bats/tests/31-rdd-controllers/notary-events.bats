load '../../helpers/load'

# Notary controller events tests - tests event generation and deduplication
# for the Notary controller including SpecUpdate, ValueRecorded, and NoChange events.
# For core controller functionality, see notary.bats

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
  namespace: default
spec:
  value: "${value}"
  configMapName: "${config_map_name}"
EOF
}

delete_notary() {
    local name=$1
    delete_resource "notary" "${name}"
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

assert_events_exist() {
    local resource_name=$1
    local reason=$2

    run -0 rdd ctl get events --field-selector involvedObject.name="${resource_name}" -o json
    run -0 jq ".items | map(select(.reason == \"${reason}\")) | length" <<<"${output}"
    refute_output 0
}

wait_for_events() {
    local resource_name=$1
    local reason=$2

    # couldn't figure out a way to use `wait` for events
    try --max 10 --delay 1 -- assert_events_exist "${resource_name}" "${reason}"
}

get_events_after_timestamp() {
    local resource_name=$1
    local timestamp=$2
    local reason=$3

    run rdd ctl get events --field-selector involvedObject.name="${resource_name}" -o json
    if [[ ${status} -ne 0 ]]; then
        echo "[]"
        return 0
    fi

    jq -r ".items | map(select(.lastTimestamp > \"${timestamp}\" and .reason == \"${reason}\"))" <<<"${output}"
}

assert_events_after_timestamp() {
    local resource_name=$1
    local timestamp=$2
    local reason=$3

    run -0 get_events_after_timestamp "${resource_name}" "${timestamp}" "${reason}"
    run -0 jq 'length' <<<"${output}"
    refute_output 0
}

wait_for_events_after_timestamp() {
    local resource_name=$1
    local timestamp=$2
    local reason=$3

    try --max 20 --delay 1 -- assert_events_after_timestamp "${resource_name}" "${timestamp}" "${reason}"
}

get_latest_event_timestamp() {
    local resource_name=$1

    run rdd ctl get events --field-selector involvedObject.name="${resource_name}" -o json
    if [[ ${status} -ne 0 ]]; then
        echo ""
        return 1
    fi

    jq -r ".items | sort_by(.lastTimestamp) | .[-1].lastTimestamp // empty" <<<"${output}"
}

@test 'verify event generation for spec updates' {
    if is_ci && is_linux; then
        skip "This test randomly fails in GitHub CI on Linux"
    fi

    create_notary "events" "initial-event-value" "events-history"

    # Wait for initial ConfigMap creation and events
    wait_for_resource_count "configmaps" "${NOTARY_CONTROLLER_NAME}" "events" 1
    wait_for_events "events" "SpecUpdate"
    wait_for_events "events" "ValueRecorded"

    # Check initial events - should have SpecUpdate and ValueRecorded events
    run -0 rdd ctl get events --field-selector involvedObject.name=events
    assert_output --partial "SpecUpdate"
    assert_output --partial "initial-event-value"
    assert_output --partial "ValueRecorded"

    # Get the timestamp of the most recent event before update
    run -0 get_latest_event_timestamp "events"
    timestamp=${output}

    # Update with a different value
    update_notary_value "events" "new-event-value"

    # Wait for status update and new events
    wait_for_notary_status "events" "new-event-value"
    wait_for_events_after_timestamp "events" "${timestamp}" "SpecUpdate"
    wait_for_events_after_timestamp "events" "${timestamp}" "ValueRecorded"

    # Verify we have new SpecUpdate and ValueRecorded events containing the new value
    run -0 get_events_after_timestamp "events" "${timestamp}" "SpecUpdate"
    assert_output --partial "new-event-value"
    run -0 get_events_after_timestamp "events" "${timestamp}" "ValueRecorded"
    assert_output --partial "new-event-value"
}

@test 'test no-change events with annotation updates' {
    skip_on_windows "This test currently fails on Windows."
    create_notary "dupe" "constant-value" "dupe-history"

    # Wait for initial ConfigMap creation and events
    wait_for_resource_count "configmaps" "${NOTARY_CONTROLLER_NAME}" "dupe" 1
    wait_for_events "dupe" "SpecUpdate"
    wait_for_events "dupe" "ValueRecorded"

    # Get the timestamp before triggering multiple identical reconciles
    run -0 get_latest_event_timestamp "dupe"
    timestamp=${output}

    # Sleep to ensure timestamp ordering (events created after this will have later timestamps)
    sleep 1

    # Trigger multiple reconciles with identical spec.value by changing annotations
    # Each will generate identical SpecUpdate and NoChange events that Kubernetes should deduplicate
    run -0 rdd ctl annotate notary dupe test-annotation-1=value1
    run -0 rdd ctl annotate notary dupe test-annotation-2=value2
    run -0 rdd ctl annotate notary dupe test-annotation-3=value3
    run -0 rdd ctl annotate notary dupe test-annotation-4=value4

    # Wait for NEW events to be generated after the timestamp
    # This ensures the controller has processed the annotation updates
    wait_for_events_after_timestamp "dupe" "${timestamp}" "SpecUpdate"
    wait_for_events_after_timestamp "dupe" "${timestamp}" "NoChange"

    # Count events after the timestamp - should be deduplicated by Kubernetes
    # It is not clear if Kubernetes will always catch all duplicates, but it should get at least 1
    run -0 get_events_after_timestamp "dupe" "${timestamp}" "SpecUpdate"
    assert_output --partial "constant-value"

    run -0 jq 'length' <<<"${output}"
    assert_output_lt 4

    run -0 get_events_after_timestamp "dupe" "${timestamp}" "NoChange"
    assert_output --partial "value unchanged"

    run -0 jq 'length' <<<"${output}"
    assert_output_lt 4

    run -0 get_events_after_timestamp "dupe" "${timestamp}" "ValueRecorded"
    run -0 jq 'length' <<<"${output}"
    assert_output 0
}
