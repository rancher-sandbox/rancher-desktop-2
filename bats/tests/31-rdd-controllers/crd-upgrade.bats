load '../../helpers/load'

# Verify that the controller updates existing CRDs on startup.
# Applies a notary CRD with a missing field, starts the controller,
# and confirms the schema is updated.

# Note: This test requires bin/rdd-controller to be built.
# Run 'make build-rdd-controller' before running this test.

local_setup_file() {
    rdd svc delete
    rdd svc create --controllers=""
    rdd svc start
}

assert_process_exited() {
    local pid=$1
    ! kill -0 "${pid}" 2>/dev/null
}

assert_crd_has_change_count() {
    local expected=$1
    run_e -0 rdd ctl get crd notaries.rdd.rancherdesktop.io --output json
    run -0 jq_output '.spec.versions[0].schema.openAPIV3Schema.properties.status.properties | has("changeCount")'
    assert_output "${expected}"
}

@test "control plane is running" {
    run -0 rdd ctl get namespaces --output name
    assert_line namespace/default
}

@test "apply old CRD without changeCount field" {
    # Apply a stripped-down notary CRD identical to the real one but with the
    # changeCount property removed from status.properties. The owner label is
    # omitted; the update path handles adding it.
    rdd ctl apply --filename - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  annotations:
    controller-gen.kubebuilder.io/version: v0.19.0
  name: notaries.rdd.rancherdesktop.io
spec:
  group: rdd.rancherdesktop.io
  names:
    kind: Notary
    listKind: NotaryList
    plural: notaries
    singular: notary
  scope: Namespaced
  versions:
  - name: v1alpha1
    schema:
      openAPIV3Schema:
        description: Notary is the Schema for the notaries API.
        properties:
          apiVersion:
            description: |-
              APIVersion defines the versioned schema of this representation of an object.
              Servers should convert recognized schemas to the latest internal value, and
              may reject unrecognized values.
              More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#resources
            type: string
          kind:
            description: |-
              Kind is a string value representing the REST resource this object represents.
              Servers may infer this from the endpoint the client submits requests to.
              Cannot be updated.
              In CamelCase.
              More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds
            type: string
          metadata:
            type: object
          spec:
            description: NotarySpec defines the desired state of Notary.
            properties:
              configMapName:
                description: ConfigMapName is the name of the ConfigMap where the
                  history will be stored
                type: string
              value:
                description: Value is the field that will be tracked for changes
                type: string
            required:
            - configMapName
            - value
            type: object
          status:
            description: NotaryStatus defines the observed state of Notary.
            properties:
              configMapStatus:
                description: ConfigMapStatus indicates the status of the ConfigMap
                  operation
                type: string
              lastRecordedValue:
                description: LastRecordedValue is the last value that was recorded
                  in the ConfigMap
                type: string
            type: object
        type: object
    served: true
    storage: true
    subresources:
      status: {}
EOF

    # Wait for the CRD to become established
    try --max 10 --delay 1 -- rdd ctl wait crd notaries.rdd.rancherdesktop.io --for condition=Established --timeout=1s
}

@test "old CRD schema lacks changeCount" {
    assert_crd_has_change_count false
}

@test "external controller updates CRD on startup" {
    "rdd-controller${EXE}" >"${BATS_FILE_TMPDIR}/controller.log" 2>&1 &
    echo "$!" >"${BATS_FILE_TMPDIR}/controller_pid"

    # Wait for controller registration
    try --max 20 --delay 1 -- rdd ctl get configmap rdd-controller-manager --namespace rdd-system

    # Verify the CRD schema now includes changeCount
    try --max 10 --delay 1 -- assert_crd_has_change_count true
}

@test "controller shuts down with control plane" {
    controller_pid=$(cat "${BATS_FILE_TMPDIR}/controller_pid")
    kill -0 "${controller_pid}"

    trace "# Stopping control plane at $(date +%T)"
    rdd svc stop
    trace "# Control plane stopped at $(date +%T), waiting for controller exit"

    if ! try --max 30 --delay 1 -- assert_process_exited "${controller_pid}"; then
        trace "# Controller did not exit in time. Log contents:"
        trace "$(cat "${BATS_FILE_TMPDIR}/controller.log" || true)"
        return 1
    fi
    trace "# Controller exited at $(date +%T)"
}
