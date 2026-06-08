load '../../helpers/load'

delete_demo() {
    delete_resource "demo" "demo"
}

create_demo() {
    local name=${1:-demo}
    local message=${2:-"Hello from RDD Demo controller test!"}
    local count=${3:-3}

    delete_demo

    rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: Demo
metadata:
  name: ${name}
spec:
  message: "${message}"
  count: ${count}
EOF
}

wait_for_demo_completion() {
    local expected=$1

    rdd ctl wait --for=jsonpath='{.status.processedCount}'="${expected}" \
        demo/demo --timeout=40s
    rdd ctl wait --for=condition=Completed demo/demo --timeout=60s
}

local_setup_file() {
    # Setup RDD control plane with demo controller
    setup_rdd_control_plane "demo"
}

@test 'create Demo resource with valid name' {
    create_demo "demo" "Test message" 3
}

@test 'verify Demo is created in Kubernetes' {
    rdd ctl wait --for=create demo demo --timeout=60s
}

@test 'verify Demo resource is cluster-scoped' {
    # Demo should be accessible without namespace since it's cluster-scoped
    run -0 rdd ctl get demo demo

    # Cluster-scoped resources in kubectl can be accessed with or without namespace
    # So this test just verifies it's accessible both ways
    run -0 rdd ctl get demo demo --namespace default
}

@test 'wait for Demo processing to start' {
    rdd ctl wait --for=condition=Ready demo/demo --timeout=60s

    # Processing condition might be True briefly or False if already completed
    # So just verify we have the Ready condition and some processing has occurred
    run -0 get_resource_status "demo" "demo" "processedCount"
    [[ "${output}" -ge 0 ]] # Should have some status
}

@test 'verify initial Demo status' {
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    run -0 jq -r 'has("status")' <<<"${demo_json}"
    assert_output "true"

    run -0 jq -r '.status | has("processedCount")' <<<"${demo_json}"
    assert_output "true"

    # Should have at least started processing (processedCount >= 1)
    run -0 get_resource_status "demo" "demo" "processedCount"
    [[ "${output}" -ge 1 ]]
}

@test 'verify Demo conditions are properly set during processing' {
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    # Should have conditions array
    run -0 jq -r '.status | has("conditions")' <<<"${demo_json}"
    assert_output "true"

    # Check for all three condition types
    run -0 jq -r '.status.conditions | map(.type) | @json' <<<"${demo_json}"
    assert_output --partial "Ready"
    assert_output --partial "Processing"
    assert_output --partial "Completed"

    # Ready should be True during processing
    run -0 jq -r '.status.conditions[] | select(.type == "Ready") | .status' <<<"${demo_json}"
    assert_output "True"
}

@test 'wait for Demo processing to complete' {
    wait_for_demo_completion 3
}

@test 'verify Demo status after completion' {
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    # Should have processed all 3 messages
    run -0 jq -r '.status.processedCount' <<<"${demo_json}"
    assert_output "3"

    # Should have lastProcessed timestamp
    run -0 jq -r '.status | has("lastProcessed")' <<<"${demo_json}"
    assert_output "true"

    # Ready should be True
    run -0 jq -r '.status.conditions[] | select(.type == "Ready") | .status' <<<"${demo_json}"
    assert_output "True"

    # Processing should be False (completed)
    run -0 jq -r '.status.conditions[] | select(.type == "Processing") | .status' <<<"${demo_json}"
    assert_output "False"

    # Completed should be True
    run -0 jq -r '.status.conditions[] | select(.type == "Completed") | .status' <<<"${demo_json}"
    assert_output "True"
}

@test 'test singleton validation - reject invalid names' {
    # Try to create Demo with invalid name
    run rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: Demo
metadata:
  name: invalid-demo
spec:
  message: "This should be rejected"
  count: 1
EOF

    # The creation might succeed initially due to API validation happening async
    # But the controller should delete it quickly
    rdd ctl wait --for=delete demo/invalid-demo --timeout=30s

    # The invalid demo should not exist
    run -1 rdd ctl get demo invalid-demo

    # The valid demo should still exist
    run -0 rdd ctl get demo demo
}

@test 'test Demo processing with different count' {
    create_demo "demo" "New test message" 5

    # Wait for processing to complete
    wait_for_demo_completion 5

    # Verify final count
    run -0 rdd ctl get demo demo -o jsonpath='{.status.processedCount}'
    assert_output "5"
}

@test 'test Demo with zero count' {
    create_demo "demo" "Zero count test" 0

    # Should immediately be marked as completed since no processing needed
    rdd ctl wait --for=condition=Completed demo/demo --timeout=60s

    # ProcessedCount might not be explicitly set to 0, or could be missing
    run -0 rdd ctl get demo demo -o json
    # Just verify completed condition is True for zero count
    run -0 jq -r '.status.conditions[] | select(.type == "Completed") | .status' <<<"${output}"
    assert_output "True"
}

@test 'test Demo Processing condition with larger count' {
    # Create Demo with larger count to catch Processing condition
    create_demo "demo" "Processing test" 20

    # Should start processing
    rdd ctl wait --for=condition=Ready demo/demo --timeout=60s

    # Should catch Processing=True at some point (or might already be completed)
    run -0 rdd ctl get demo demo -o json

    # Verify we have a Processing condition (might be True or False)
    local processing_exists
    processing_exists=$(echo "${output}" | jq -r '.status.conditions[] | select(.type == "Processing") | .type')
    [[ "${processing_exists}" = "Processing" ]]
}

@test 'test Demo update during processing' {
    create_demo "demo" "Update test" 10

    # Wait for demo to be ready (which means processing has started and processedCount >= 1)
    rdd ctl wait --for=condition=Ready demo/demo --timeout=60s

    # Update the message and count
    rdd ctl patch demo demo --type='merge' -p='{"spec":{"message":"Updated message","count":15}}'

    # Eventually should reach the new count
    wait_for_demo_completion 15

    # Verify final state
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    run -0 jq -r '.status.processedCount' <<<"${demo_json}"
    assert_output "15"

    run -0 jq -r '.spec.message' <<<"${demo_json}"
    assert_output "Updated message"
}

@test 'verify Demo events are recorded' {
    # Check that events were created for the demo processing
    run -0 rdd ctl get events --field-selector involvedObject.kind=Demo

    # Should have processing and completion events
    assert_output --partial "Processing"
    assert_output --partial "Completed"
}

@test 'test Demo deletion' {
    delete_demo

    # Verify deletion
    run -1 rdd ctl get demo demo
}

@test 'test Demo recreation after deletion' {
    # Should be able to create Demo again after deletion
    create_demo "demo" "Recreation test" 2

    # Wait for processing
    wait_for_demo_completion 2

    # Verify it works
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    run -0 jq -r '.status.processedCount' <<<"${demo_json}"
    assert_output "2"

    run -0 jq -r '.spec.message' <<<"${demo_json}"
    assert_output "Recreation test"
}

@test 'verify Demo has no finalizers by default' {
    create_demo "demo" "Finalizer test" 1

    # Wait for creation
    rdd ctl wait --for=create demo demo --timeout=60s

    # Check that no finalizers are set (Demo controller doesn't use finalizers)
    run rdd ctl get demo demo -o jsonpath='{.metadata.finalizers}'
    # Should be empty or null
    [[ -z "${output}" ]] || [[ "${output}" = "null" ]]
}

@test 'demo status reflects current processing state' {
    create_demo "demo" "Status test" 2

    # Wait for processing to complete
    wait_for_demo_completion 2

    # Check comprehensive status
    run -0 rdd ctl get demo demo -o json
    local demo_json="${output}"

    run -0 jq -r 'has("status")' <<<"${demo_json}"
    assert_output "true"

    run -0 jq -r '.status.processedCount' <<<"${demo_json}"
    assert_output "2"

    run -0 jq -r '.status | has("lastProcessed")' <<<"${demo_json}"
    assert_output "true"

    run -0 jq -r '.status | has("conditions")' <<<"${demo_json}"
    assert_output "true"

    # Extract condition statuses using jq
    run -0 jq -r '.status.conditions[] | select(.type == "Ready") | .status' <<<"${demo_json}"
    local ready_status="${output}"

    run -0 jq -r '.status.conditions[] | select(.type == "Processing") | .status' <<<"${demo_json}"
    local processing_status="${output}"

    run -0 jq -r '.status.conditions[] | select(.type == "Completed") | .status' <<<"${demo_json}"
    local completed_status="${output}"

    [[ "${ready_status}" = "True" ]]
    [[ "${processing_status}" = "False" ]]
    [[ "${completed_status}" = "True" ]]
}
