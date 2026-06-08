load '../../helpers/load'

CONFIGMAP_CONTROLLER_NAME="rdd-configmapreplicaset"

local_setup_file() {
    setup_rdd_control_plane "configmapreplicaset"
}

create_configmapreplicaset() {
    local name=$1
    local replicas=$2

    rdd ctl apply -f - <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: ConfigMapReplicaSet
metadata:
  name: ${name}
  namespace: ${RDD_NAMESPACE}
spec:
  replicas: ${replicas}
  data:
    config.yaml: |
      app:
        name: "test-app"
        version: "1.0.0"
        debug: true
      database:
        host: "localhost"
        port: 5432
        name: "test_db"
    app.properties: |
      server.port=8080
      logging.level.root=INFO
EOF
}

wait_for_configmaps() {
    local name=$1
    local expected=$2
    wait_for_resource_count "configmaps" "${CONFIGMAP_CONTROLLER_NAME}" "${name}" "${expected}"
}

@test 'create ConfigMapReplicaSet resource' {
    rdd ctl create namespace "${RDD_NAMESPACE}" || true
    create_configmapreplicaset "basic" 3
}

@test 'verify ConfigMapReplicaSet is created in Kubernetes' {
    rdd ctl wait --for=create ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic --timeout=15s
}

@test 'wait for controller to create 3 ConfigMaps' {
    wait_for_configmaps "basic" 3
}

@test 'verify ConfigMaps have correct names' {
    run -0 rdd ctl get configmaps --namespace "${RDD_NAMESPACE}" -o json \
        -l app.kubernetes.io/managed-by=rdd-configmapreplicaset,app.kubernetes.io/instance=basic
    run -0 jq_output '.items[].metadata.name'
    assert_line "basic-0"
    assert_line "basic-1"
    assert_line "basic-2"
}

@test 'verify ConfigMap content includes original data plus index' {
    # Check first ConfigMap contains original data plus index
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 -o json
    local basic0_json="${output}"

    run -0 jq -r '.data | keys[]' <<<"${basic0_json}"
    assert_line "config.yaml"
    assert_line "app.properties"
    assert_line "index"

    run -0 jq -r '.data."config.yaml"' <<<"${basic0_json}"
    assert_output --partial "test-app"

    run -0 jq -r '.data."app.properties"' <<<"${basic0_json}"
    assert_output --partial "server.port=8080"

    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 -o jsonpath='{.data.index}'
    assert_output "0"

    # Check second ConfigMap has different index
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-1 -o jsonpath='{.data.index}'
    assert_output "1"
}

@test 'verify ConfigMap owner references are set' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 -o json
    local configmap_json="${output}"

    # Verify owner reference exists and has correct kind
    run -0 jq -r '.metadata.ownerReferences[0].kind' <<<"${configmap_json}"
    assert_output "ConfigMapReplicaSet"

    run -0 jq -r '.metadata.ownerReferences[0].name' <<<"${configmap_json}"
    assert_output "basic"

    # Verify the controller reference is set for garbage collection
    run -0 jq -r '.metadata.ownerReferences[0].controller' <<<"${configmap_json}"
    assert_output "true"

    run -0 jq -r '.metadata.ownerReferences[0].blockOwnerDeletion' <<<"${configmap_json}"
    assert_output "true"
}

@test 'debug owner references before deletion' {
    # Extract and validate complete owner reference structure
    run -0 rdd ctl get ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic -o json
    local parent_json="${output}"

    run -0 jq -r '.metadata.uid' <<<"${parent_json}"
    local parent_uid=${output}

    run -0 jq -r '.apiVersion' <<<"${parent_json}"
    local parent_api_version=${output}

    # Verify owner reference structure
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 -o json
    local configmap="${output}"

    # Verify critical owner reference fields
    run -0 jq -r '.metadata.ownerReferences[0].uid // empty' <<<"${configmap}"
    local ref_uid=${output}
    run -0 jq -r '.metadata.ownerReferences[0].controller // false' <<<"${configmap}"
    local ref_controller=${output}
    run -0 jq -r '.metadata.ownerReferences[0].blockOwnerDeletion // false' <<<"${configmap}"
    local ref_block_deletion=${output}
    run -0 jq -r '.metadata.ownerReferences[0].apiVersion // empty' <<<"${configmap}"
    local ref_api_version=${output}

    # Validate all fields are correct
    [[ "${ref_uid}" = "${parent_uid}" ]]
    [[ "${ref_controller}" = "true" ]]
    [[ "${ref_block_deletion}" = "true" ]]
    [[ "${ref_api_version}" = "${parent_api_version}" ]]
}

@test 'scale ConfigMapReplicaSet up to 5 replicas' {
    rdd ctl patch ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic \
        --type='merge' -p='{"spec":{"replicas":5}}'
}

@test 'wait for scaling up to complete (5 ConfigMaps)' {
    wait_for_configmaps "basic" 5
}

@test 'verify all 5 ConfigMaps exist after scaling up' {
    run -0 rdd ctl get configmaps --namespace "${RDD_NAMESPACE}" -o json \
        -l app.kubernetes.io/managed-by=rdd-configmapreplicaset,app.kubernetes.io/instance=basic
    local configmaps="${output}"

    # Verify we have exactly 5 ConfigMaps
    run -0 jq '.items | length' <<<"${configmaps}"
    assert_output "5"

    # Verify correct names
    run -0 jq -r '.items | map(.metadata.name) | sort | .[]' <<<"${configmaps}"
    assert_line "basic-0"
    assert_line "basic-1"
    assert_line "basic-2"
    assert_line "basic-3"
    assert_line "basic-4"
}

@test 'scale ConfigMapReplicaSet down to 2 replicas' {
    rdd ctl patch ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic \
        --type='merge' -p='{"spec":{"replicas":2}}'
}

@test 'wait for scaling down to complete (2 ConfigMaps)' {
    wait_for_configmaps "basic" 2
}

@test 'verify only 2 ConfigMaps remain after scaling down' {
    run -0 rdd ctl get configmaps --namespace "${RDD_NAMESPACE}" -o json \
        -l app.kubernetes.io/managed-by=rdd-configmapreplicaset,app.kubernetes.io/instance=basic
    local configmaps="${output}"

    # Verify we have exactly 2 ConfigMaps
    run -0 jq '.items | length' <<<"${configmaps}"
    assert_output "2"

    # Verify the remaining ConfigMaps are the first two
    run -0 jq -r '.items | map(.metadata.name) | sort | .[]' <<<"${configmaps}"
    assert_line "basic-0"
    assert_line "basic-1"
}

@test 'update ConfigMapReplicaSet data' {
    # Update the ConfigMapReplicaSet data
    rdd ctl patch ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic \
        --type='merge' -p='{"spec":{"data":{"config.yaml":"updated: true\n''version: 2.0.0\n","new-file.txt":"This is a new file"}}}'

    # Increase replica count from 2 to 4 to create new replicas with updated data
    rdd ctl patch ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic \
        --type='merge' -p='{"spec":{"replicas":4}}'

    # Wait for new ConfigMaps to be created
    wait_for_configmaps "basic" 4

    # Verify old ConfigMaps (0,1) still have original data
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 -o json
    local basic0_json="${output}"

    run -0 jq -r '.data."config.yaml"' <<<"${basic0_json}"
    assert_output --partial "test-app"
    refute_output --partial "updated: true"
    refute_output --partial "version: 2.0.0"

    run -0 jq -r '.data."app.properties"' <<<"${basic0_json}"
    assert_output --partial "server.port=8080"

    run -0 jq -r '.data.index' <<<"${basic0_json}"
    assert_output "0"

    run -0 jq -r '.data | has("new-file.txt")' <<<"${basic0_json}"
    assert_output "false"

    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-1 -o json
    local basic1_json="${output}"

    run -0 jq -r '.data."config.yaml"' <<<"${basic1_json}"
    assert_output --partial "test-app"
    refute_output --partial "updated: true"
    refute_output --partial "version: 2.0.0"

    run -0 jq -r '.data."app.properties"' <<<"${basic1_json}"
    assert_output --partial "server.port=8080"

    run -0 jq -r '.data.index' <<<"${basic1_json}"
    assert_output "1"

    run -0 jq -r '.data | has("new-file.txt")' <<<"${basic1_json}"
    assert_output "false"

    # Verify new ConfigMaps (2,3) have updated data
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-2 -o json
    local basic2_json="${output}"

    run -0 jq -r '.data."config.yaml"' <<<"${basic2_json}"
    assert_output --partial "updated: true"
    assert_output --partial "version: 2.0.0"

    run -0 jq -r '.data."new-file.txt"' <<<"${basic2_json}"
    assert_output "This is a new file"

    run -0 jq -r '.data.index' <<<"${basic2_json}"
    assert_output "2"

    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-3 -o json
    local basic3_json="${output}"

    run -0 jq -r '.data."config.yaml"' <<<"${basic3_json}"
    assert_output --partial "updated: true"
    assert_output --partial "version: 2.0.0"

    run -0 jq -r '.data."new-file.txt"' <<<"${basic3_json}"
    assert_output "This is a new file"

    run -0 jq -r '.data.index' <<<"${basic3_json}"
    assert_output "3"
}

@test 'verify ConfigMapReplicaSet has finalizer for cleanup' {
    # Check that the ConfigMapReplicaSet has the cleanup finalizer
    # Check ConfigMap finalizers (should be empty)
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" basic-0 \
        -o jsonpath='{.metadata.finalizers}'
    refute_output

    # Check parent resource finalizers (should have shared cleanup finalizer)
    run -0 rdd ctl get ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic \
        -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test 'delete ConfigMapReplicaSet' {
    delete_resource "ConfigMapReplicaSet" "basic"
}

@test 'verify parent resource deletion triggers finalizer cleanup' {
    # Parent should be deleted by the finalizer after ConfigMaps are cleaned up
    # The finalizer should handle cleanup automatically
    run -1 rdd ctl get ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" basic
}

@test 'wait for ConfigMaps to be cleaned up by finalizer' {
    wait_for_configmaps "basic" 0
}

@test 'create ConfigMapReplicaSet with zero replicas' {
    create_configmapreplicaset "zero" 0
}

@test 'verify zero replicas ConfigMapReplicaSet is created' {
    rdd ctl wait --for=create ConfigMapReplicaSet \
        --namespace "${RDD_NAMESPACE}" zero --timeout=60s
}

@test 'verify no ConfigMaps are created for zero replicas' {
    assert_resource_count "configmaps" "${CONFIGMAP_CONTROLLER_NAME}" "zero" 0
}

@test 'create ConfigMapReplicaSet with single replica' {
    create_configmapreplicaset "single" 1
}

@test 'wait for single ConfigMap to be created' {
    wait_for_configmaps "single" 1
}

@test 'verify single ConfigMap has correct name' {
    run -0 rdd ctl get configmaps --namespace "${RDD_NAMESPACE}" -o json \
        -l app.kubernetes.io/managed-by=rdd-configmapreplicaset,app.kubernetes.io/instance=single
    local configmaps="${output}"

    # Verify we have exactly 1 ConfigMap
    run -0 jq '.items | length' <<<"${configmaps}"
    assert_output "1"

    # Verify correct name
    run -0 jq -r '.items[0].metadata.name' <<<"${configmaps}"
    assert_output "single-0"
}

@test 'verify single ConfigMap has correct index' {
    run -0 rdd ctl get configmap --namespace "${RDD_NAMESPACE}" single-0 -o jsonpath='{.data.index}'
    assert_output "0"
}

@test 'configMapReplicaSet status reflects current state' {
    create_configmapreplicaset "status" 3

    # Wait for the configmaps to be created
    wait_for_configmaps "status" 3

    # Check the status of the ConfigMapReplicaSet
    run -0 rdd ctl get ConfigMapReplicaSet --namespace "${RDD_NAMESPACE}" status \
        -o json
    run -0 jq -r 'has("status")' <<<"${output}"
    assert_output "true"
}
