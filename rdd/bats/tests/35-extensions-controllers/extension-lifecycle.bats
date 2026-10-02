# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Extension controller integration tests:
# Verify building testing image locally, installation, lifecycle state transitions,
# running containers, and clean uninstallation.

TEST_EXTENSION_IMAGE="example.com/extension/everything:latest"
EXTENSION_NAME="test-extension"

local_setup_file() {
    export RDD_NAMESPACE=${RDD_APP_NAMESPACE}
    start_docker_engine

    # Make sure our plugins are available.
    rdd set application.addPath=front

    # Wait for the engine to be ready
    wait_for_resource_condition App app ContainerEngineReady reason Connected
    wait_for_resource_condition App app PathManagementReady status True
}

local_teardown_file() {
    delete_resource extension "${EXTENSION_NAME}"
    ctrctl rmi "${TEST_EXTENSION_IMAGE}" 2>/dev/null || true
}

local_setup() {
    docker_has_build || fail "docker buildx is not available"
}

@test "build extension testing image" {
    cd "${BATS_TEST_DIRNAME}/testdata"
    rdd run docker build \
        --tag "${TEST_EXTENSION_IMAGE}" \
        --build-arg variant=everything \
        .
}

@test "install extension and verify lifecycle status conditions" {
    rdd ctl apply -f - <<EOF
apiVersion: extensions.rancherdesktop.io/v1alpha1
kind: Extension
metadata:
  name: ${EXTENSION_NAME}
  namespace: ${RDD_APP_NAMESPACE}
spec:
  image: ${TEST_EXTENSION_IMAGE}
EOF

    # Verify transitions through extraction and startup to Ready: True
    wait_for_resource_condition Extension "${EXTENSION_NAME}" Installed status True
    wait_for_resource_condition Extension "${EXTENSION_NAME}" Extracted status True
    wait_for_resource_condition Extension "${EXTENSION_NAME}" Started status True
    wait_for_resource_condition Extension "${EXTENSION_NAME}" Ready status True

    # Verify status.image and status.ui fields are populated
    run -0 get_resource_status Extension "${EXTENSION_NAME}" image
    assert_output "${TEST_EXTENSION_IMAGE}"

    run -0 get_resource_status Extension "${EXTENSION_NAME}" ui.dashboardTab.title
    assert_output "Sample Extension With Everything"
}

@test "installed extension has running compose project" {
    wait_for_resource_condition Extension "${EXTENSION_NAME}" Started status True

    run -0 --separate-stderr rdd ctl get ComposeProjects --namespace "${RDD_APP_NAMESPACE}" \
        --output json
    run -0 jq_output ".items[]
        | select(.metadata.ownerReferences[] | .kind == \"Extension\" and .name == \"${EXTENSION_NAME}\")
        | .metadata.name"
    projectName=${output}

    run -0 rdd ctl get "ComposeProject/${projectName}" --namespace "${RDD_APP_NAMESPACE}" \
        --output jsonpath='{.status.name}'
    assert_output "rdx-${EXTENSION_NAME}"

    assert_resource_condition ComposeProject "${projectName}" HasMembers status True
}

@test "uninstall extension and verify complete resource removal" {
    rdd ctl delete extension "${EXTENSION_NAME}" --namespace "${RDD_APP_NAMESPACE}" \
        --timeout=60s

    run rdd ctl get extension "${EXTENSION_NAME}" --namespace "${RDD_APP_NAMESPACE}"
    assert_failure

    run -0 rdd ctl get ComposeProjects --namespace "${RDD_APP_NAMESPACE}" \
        --output jsonpath='{.items[*].spec.name}'
    refute_output --partial "rdx-${EXTENSION_NAME}"
}
