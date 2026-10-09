# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# For the moby backend, container namespaces are not supported; all objects are
# always in the "moby" namespace.
CONTAINER_NAMESPACE="moby"

# The annotation key used to allow overriding the reap delay for Compose and
# ComposeUpRequest objects.
REAP_ANNOTATION="containers.rancherdesktop.io/reap-after"

local_setup_file() {
    skip_unless_docker
    # Disable the test if we don't have `docker compose`; this needs to go away
    # once we can ensure the reconciler can find `docker compose` correctly,
    # since the test does not use it directly.
    docker_has_compose || skip "docker compose plugin is not installed"
    start_docker_engine
    # Pre-pull the image
    docker pull --quiet "${IMAGE_BUSYBOX}"
    RDD_NAMESPACE=$(rdd ctl get app/app -o jsonpath='{.spec.namespace}')
    export RDD_NAMESPACE
}

# compose_project_name returns the deterministic metadata.name for a
# ComposeProject (or ComposeUpRequest) with the given project name, matching
# generateProjectName(): the candidate name is
# "${CONTAINER_NAMESPACE}.<name>"; if that is a valid Kubernetes name, it is
# used directly, otherwise this is "cmp-" followed by the SHA-256 hash of the
# candidate name.
#
# This only implements the "candidate name is already valid" branch: every
# caller in this file passes a lower-case alphanumeric-and-dash project name,
# so the candidate (joined to CONTAINER_NAMESPACE, itself always "moby") is
# always already a valid Kubernetes name and never needs the hash fallback.
compose_project_name() { # <valid-k8s-name-project>
    if [[ ! "${1}" =~ ^[-a-z0-9]+$ ]]; then
        fail "Unsupported project name ${1}"
    fi
    printf '%s.%s' "${CONTAINER_NAMESPACE}" "${1}"
}

@test "a container with compose labels is auto-detected into a Compose with member tracking" {
    local project=rdd-bats-detect
    run_e -0 docker run --detach --name test-compose-detect \
        --label "com.docker.compose.project=${project}" \
        --label "com.docker.compose.config-hash=bogus" \
        "${IMAGE_BUSYBOX}" sleep 300
    local container_id=${output}

    local name
    name=$(compose_project_name "${project}")
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ComposeProject/"${name}" --timeout=30s

    rdd ctl annotate ComposeProject "${name}" --namespace="${RDD_NAMESPACE}" \
        "${REAP_ANNOTATION}=1s" --overwrite

    wait_for_resource_condition ComposeProject "${name}" HasMembers reason Found

    run -0 rdd ctl get "ComposeProject/${name}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.namespace}/{.status.name}'
    assert_output "${CONTAINER_NAMESPACE}/${project}"

    run -0 rdd ctl get "ComposeProject/${name}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.containers[*].name}'
    assert_output "${container_id}"

    # Test that the Compose object is reaped.
    docker rm --force "${container_id}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        "ComposeProject/${name}" --timeout=30s
}

@test "ComposeUpRequest runs docker compose up for the project's own configs" {
    local project=rdd-bats-custom-configs
    # Deliberately a custom compose file name, so it can only be found via
    # spec.configs.
    cat >"${BATS_TEST_TMPDIR}/app-stack.yaml" <<EOF
services:
  app:
    image: ${IMAGE_BUSYBOX}
    command: sleep inf
EOF
    run -0 host_path "${BATS_TEST_TMPDIR}"
    local working_dir=${output}

    local name
    name=$(compose_project_name "${project}")

    rdd ctl apply -f - <<EOF
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ComposeUpRequest
metadata:
  name: ${name}
  namespace: ${RDD_NAMESPACE}
spec:
  namespace: ${CONTAINER_NAMESPACE}
  name: ${project}
  workingDir: ${working_dir}
  configs:
    - app-stack.yaml
EOF

    wait_for_resource_condition ComposeUpRequest "${name}" Settled status True

    # The resulting container triggers the Compose reconciler, which creates a
    # matching Compose object with the same deterministic name.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ComposeProject/"${name}" --timeout=30s

    rdd ctl annotate ComposeProject "${name}" --namespace="${RDD_NAMESPACE}" \
        "${REAP_ANNOTATION}=1s" --overwrite

    run -0 docker ps --filter "label=com.docker.compose.project=${project}" \
        --quiet
    assert_output
    container_id=${output}

    run -0 docker inspect "${container_id}" --format '{{.State.Status}}'
    assert_output "running"

    # The ComposeUpRequest is reaped automatically some time after it completes;
    # we don't wait for that here; just clean up the container it created.
    docker rm --force "${container_id}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        ComposeProject/"${name}" --timeout=30s
}

@test "deleting a Compose runs docker compose down for its resources" {
    local project=rdd-bats-down
    run_e -0 docker run --detach --name test-compose-down \
        --label "com.docker.compose.project=${project}" \
        --label "com.docker.compose.config-hash=bogus" \
        "${IMAGE_BUSYBOX}" sleep inf
    local container_id=${output}

    local name
    name=$(compose_project_name "${project}")
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ComposeProject/"${name}" --timeout=30s
    rdd ctl annotate ComposeProject "${name}" --namespace="${RDD_NAMESPACE}" \
        "${REAP_ANNOTATION}=1s" --overwrite

    run -0 rdd ctl delete "ComposeProject/${name}" --namespace="${RDD_NAMESPACE}" \
        --wait=false

    # The container should be (eventually) removed.
    try --until-fail docker inspect "${container_id}" --format '{{.State.Status}}'
    echo "Expected 'error: no such object: ${container_id}' on previous line"

    # The Compose object may _already_ be gone, which means we cannot use
    # `rdd ctl wait`.  We will need to poll instead.
    try --until-fail rdd ctl get ComposeProject/"${name}" --namespace="${RDD_NAMESPACE}"
}

@test "Compose is not created for a container without the compose project label" {
    run_e -0 docker run --detach --name test-compose-unlabeled \
        "${IMAGE_BUSYBOX}" sleep inf
    local container_id=${output}

    run -0 rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        "container/${container_id}" --timeout=30s

    # No compose labels, so no Compose object should ever appear for this
    # container: give the reconciler a moment, then assert nothing was created.
    sleep 2
    run -0 rdd ctl get ComposeProject --namespace="${RDD_NAMESPACE}" \
        -o jsonpath="{range .items[*]}{.status.containers[*].name}{'\n'}{end}"
    refute_output --partial "${container_id}"

    docker rm --force "${container_id}"
}

# assert_members_count verifies that the members array in status has the expected length.
assert_members_count() { # <name> <expected_count>
    local name=$1 expected_count=$2
    run -0 rdd ctl get "ComposeProject/${name}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.containers}'
    jq_output 'length'
    assert_output "${expected_count}"
}

@test "multiple containers with the same compose project label are auto-detected and tracked concurrently" {
    local project=rdd-bats-multi-detect
    run_e -0 docker run --detach --name test-compose-multi-1 \
        --label "com.docker.compose.project=${project}" \
        --label "com.docker.compose.config-hash=bogus" \
        "${IMAGE_BUSYBOX}" sleep inf
    local container_id1=${output}

    run_e -0 docker run --detach --name test-compose-multi-2 \
        --label "com.docker.compose.project=${project}" \
        --label "com.docker.compose.config-hash=bogus" \
        "${IMAGE_BUSYBOX}" sleep inf
    local container_id2=${output}

    local name
    name=$(compose_project_name "${project}")
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ComposeProject/"${name}" --timeout=30s

    rdd ctl annotate ComposeProject "${name}" --namespace="${RDD_NAMESPACE}" \
        "${REAP_ANNOTATION}=1s" --overwrite

    # Wait until both members are tracked in status
    try --max 30 --delay 1 -- assert_members_count "${name}" 2

    # Stop one container; members should drop to 1
    docker rm --force "${container_id1}"
    try --max 30 --delay 1 -- assert_members_count "${name}" 1

    # Stop the second container; project should be deleted
    docker rm --force "${container_id2}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        "ComposeProject/${name}" --timeout=30s
}
