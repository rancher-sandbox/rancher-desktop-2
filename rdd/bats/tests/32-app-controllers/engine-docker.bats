# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Engine controller tests — verify that the engine controller mirrors Docker
# containers, images, and volumes into Container, Image, and Volume
# resources, and that deletions and action annotations on Container
# resources are forwarded to Docker.

CONTEXT_NAME="rancher-desktop-${RDD_INSTANCE}"

local_setup_file() {
    # Isolate all Docker config reads and writes from the developer's real
    # ~/.docker directory. Set DOCKER_CONFIG before starting the service so
    # the controller process inherits it (service.Start uses exec.Command
    # without an explicit Env, so it inherits the caller's environment).
    #
    # On Windows, rdd.exe is a native binary: it interprets /tmp/... as a
    # path from the drive root (C:\tmp\...) rather than MSYS2's /tmp which
    # maps to a different location. cygpath -m produces a mixed-format path
    # (C:/msys64/...) that both native Windows processes and MSYS2 agree on.
    if is_windows; then
        run -0 cygpath -m "${BATS_FILE_TMPDIR}/docker-config"
        DOCKER_CONFIG=${output}
    else
        DOCKER_CONFIG="${BATS_FILE_TMPDIR}/docker-config"
    fi
    export DOCKER_CONFIG
    mkdir -p "${DOCKER_CONFIG}"

    # Deliberately skip setup_rdd_control_plane here: `rdd set` internally
    # calls ensureServiceRunning, which is exactly the CLI path we want to
    # exercise — the engine controller is part of the default controller
    # set, so no explicit --controllers selection is needed.
    rdd svc delete
    rdd set running=true
    if is_windows; then
        export DOCKER_HOST="npipe:////./pipe/docker_engine"
    else
        run -0 rdd svc paths docker_socket
        export DOCKER_HOST="unix://${output}"
    fi
    # Mirror resources live in App.spec.namespace. Override RDD_NAMESPACE
    # to whatever the App was created with so the test queries the same
    # namespace the engine controller uses, regardless of CRD defaults.
    RDD_NAMESPACE=$(rdd ctl get app app -o jsonpath='{.spec.namespace}')
    export RDD_NAMESPACE
}

# Make a WebSocket request, with the endpoint given as the first argument in the
# same form as `kubectl get --raw`; the output is captured in `${output}`.
do_websocket() { # endpoint
    local endpoint=$1
    run_e -0 rdd ctl config view --minify --flatten --output=jsonpath='{.clusters[].cluster.server}'
    local server_url=${output}
    run_e -0 rdd ctl config view --minify --flatten --output=jsonpath='{.users[].user.token}'
    local token=${output}
    run -0 curl --header "Authorization: Bearer ${token}" --insecure --max-time 30 \
        "${server_url/http/ws}${endpoint}"
}

# --- Startup ---

@test "ContainerNamespace moby exists" {
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s
}

# --- Image mirroring ---

@test "docker pull creates Image resource" {
    docker pull busybox
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:latest" --timeout=30s
}

@test "docker image rm of one tag removes only the tag mirror" {
    # Docker's untag event carries the image ID hash, not the tag
    # name, so the engine cannot match the event payload against
    # status.repoTag directly. reconcileImageByID re-inspects the
    # image and prunes Image mirrors whose tags are no longer present
    # in Docker's current RepoTags.
    docker tag busybox:latest busybox:alias
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:alias" --timeout=30s

    # Sanity check: the original tag is still mirrored.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output

    docker image rm busybox:alias
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=busybox:alias" --timeout=30s

    # busybox:latest must remain because the image still has that tag.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output
}

@test "tagging a dangling image removes the dangling mirror" {
    # Create a dangling image by pinning it via a running container and
    # then removing its only tag with --force. Docker keeps the image
    # (the container still references it), so the UnTag path produces
    # a dangling Image mirror rather than deleting the mirror outright.
    # Tagging the dangling image then must prune the dangling mirror
    # and leave only the new tagged one — the symmetric direction of
    # the untag test above.
    docker pull alpine:latest
    run -0 docker inspect alpine:latest --format '{{.Id}}'
    alpine_id=${output}

    # The pin must be a running container: in this VM's Docker,
    # image rm --force will fully remove an image whose only references are
    # stopped containers, leaving nothing for the dangling-mirror path
    # to observe.
    docker run -d --name dangling-pin alpine:latest sleep inf

    # Remove the only tag; the running container keeps the image alive.
    docker image rm --force alpine:latest

    # The dangling mirror has no RepoTag — query by status.id instead.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.id=${alpine_id}" --timeout=30s

    # Sanity check: exactly one mirror exists for this image and it has
    # no RepoTag (the dangling mirror).
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.id=${alpine_id}" \
        -o jsonpath='{.items[*].status.repoTag}'
    refute_output

    # Re-tag the image with a fresh alias. ActionTag routes through
    # reconcileImageByID, which creates a new tagged mirror and prunes
    # the now-stale dangling mirror.
    docker tag "${alpine_id}" dangling-tag-test:v1
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=dangling-tag-test:v1" --timeout=30s

    # The dangling mirror must be gone: the only mirror for this image
    # is the new tagged one.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.id=${alpine_id}" \
        -o jsonpath='{.items[*].status.repoTag}'
    assert_output "dangling-tag-test:v1"

    # Cleanup.
    docker rm --force dangling-pin
    docker image rm dangling-tag-test:v1
}

# --- Container lifecycle mirroring ---

@test "docker run creates Container resource with status=running" {
    run_e -0 docker run -d --name test-lifecycle busybox sleep inf
    cid=${output}

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=10s

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.name}'
    assert_output "test-lifecycle"

    # Fresh mirrors carry no action history.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction}'
    refute_output
}

@test "docker stop updates Container status to exited" {
    run -0 docker inspect test-lifecycle --format '{{.Id}}'
    cid=${output}

    docker stop test-lifecycle
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
}

@test "docker rm deletes Container resource" {
    run -0 docker inspect test-lifecycle --format '{{.Id}}'
    cid=${output}

    docker rm --force test-lifecycle
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s
}

@test "ports mirror with and without host bindings" {
    # nginx has EXPOSE 80 in its Dockerfile. Running it without -p
    # yields NetworkSettings.Ports={"80/tcp":null} — an exposed but
    # unpublished port. Running it with -p 80 adds IPv4 and IPv6 host
    # bindings. The mirror must surface both cases: the port name
    # always appears, and bindings appear only when the port is
    # published.
    run_e -0 docker run -d --name test-exposed nginx
    exposed_cid=${output}
    run_e -0 docker run -d --name test-published -p 80 nginx
    published_cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${exposed_cid}" --timeout=30s
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${published_cid}" --timeout=30s

    # Both mirrors list the exposed port by name.
    run -0 rdd ctl get container "${exposed_cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.ports[*].name}'
    assert_output "80/tcp"
    run -0 rdd ctl get container "${published_cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.ports[*].name}'
    assert_output "80/tcp"

    # Unpublished: no bindings.
    run -0 rdd ctl get container "${exposed_cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.ports[?(@.name=="80/tcp")].bindings[*].hostPort}'
    refute_output

    # Published: at least one binding.
    run -0 rdd ctl get container "${published_cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.ports[?(@.name=="80/tcp")].bindings[*].hostPort}'
    assert_output

    docker rm --force test-exposed test-published
}

# --- Container logs ---

@test "docker logs for container with tty" {
    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi

    command='
        echo "hello"
        sleep 1
        echo "world"
    '
    run_e -0 docker run --detach --tty --name test-logs-tty busybox /bin/sh -c "${command}"
    container=${output}

    do_websocket "/passthrough/engine/logs/${container}"
    # `--tty` means the output is cooked, so LF has been converted to CRLF.
    # On Windows, MSYS2/Git Bash strips CR from pipe output, so we only
    # assert the CR on non-Windows platforms.
    if is_windows; then
        assert_line hello
        assert_line world
    else
        assert_line hello$'\r'
        assert_line world$'\r'
    fi
}

@test "cleanup from logs with tty test" {
    if curl_has_websocket_support; then
        docker rm --force test-logs-tty || true
    fi
}

@test "docker logs for container without tty" {
    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi

    command='
        echo "hello"
        sleep 1
        echo "world"
    '
    run_e -0 docker run --detach --tty=false --name test-logs-no-tty busybox /bin/sh -c "${command}"
    container=${output}

    do_websocket "/passthrough/engine/logs/${container}"
    # Without `--tty`, the output does not have CR inserted.
    assert_line hello
    assert_line world
}

@test "cleanup from logs without tty test" {
    if curl_has_websocket_support; then
        docker rm --force test-logs-no-tty || true
    fi
}

@test "docker log lines can be limited" {
    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi

    command='true'
    for ((i = 0; i < 10; i++)); do
        command="${command}; echo line number ${i}"
    done

    # Run the container to finish before trying to look at the logs, to ensure
    # we don't get more logs lines than expected.
    run -0 docker run --tty=false --name test-logs-limit busybox /bin/sh -c "${command}"
    # We need the full container ID; `docker ps` truncates it.
    run_e -0 docker inspect test-logs-limit --format '{{.Id}}'
    assert_output
    container=${output}

    do_websocket "/passthrough/engine/logs/${container}?tail=5"
    # We should only get the last five lines
    refute_line "line number 0"
    refute_line "line number 1"
    refute_line "line number 2"
    refute_line "line number 3"
    refute_line "line number 4"
    assert_line "line number 5"
    assert_line "line number 6"
    assert_line "line number 7"
    assert_line "line number 8"
    assert_line "line number 9"
}

@test "cleanup from log lines limit test" {
    if curl_has_websocket_support; then
        docker rm --force test-logs-limit || true
    fi
}

@test "docker log lines can disable following" {
    if ! curl_has_websocket_support; then
        skip "curl does not support WebSocket"
    fi

    command='
        echo "hello"
        sleep inf
        echo "world"
    '
    run_e -0 docker run --detach --tty=false --name test-logs-no-follow busybox /bin/sh -c "${command}"
    container=${output}

    # Wait until `sleep` runs
    try --max 30 --delay 1 -- docker top test-logs-no-follow -C sleep -o pid

    # At this point, we should have one line of output.
    do_websocket "/passthrough/engine/logs/${container}?follow=false"
    assert_line hello
    refute_line world

    # The container should still be running.
    run_e -0 docker ps --quiet --filter=name=test-logs-no-follow
    assert_output
}

@test "cleanup from log lines no follow test" {
    if curl_has_websocket_support; then
        docker rm --force test-logs-no-follow || true
    fi
}

# --- Volume mirroring ---

# Volume mirror names are derived from the Docker name via SHA-256 hashing
# (see volumeMirrorName in sync_volumes.go), so tests look up Volumes by
# status.name through the .status.name selectable field rather than by
# metadata.name.

@test "docker volume create creates Volume resource" {
    docker volume create test-vol

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=test-vol" --timeout=30s
}

@test "docker volume rm deletes Volume resource" {
    docker volume rm test-vol
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=test-vol" --timeout=30s
}

@test "volume name with uppercase and underscore is mirrored" {
    # Docker permits characters (uppercase, underscore) that are
    # invalid in RFC 1123 subdomain names. volumeK8sName hashes the
    # Docker name into a valid subdomain; the original is preserved
    # in status.name and queryable via the field selector.
    docker volume create My_Vol_Ume
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=My_Vol_Ume" --timeout=30s
    docker volume rm My_Vol_Ume
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" volume \
        --field-selector "status.name=My_Vol_Ume" --timeout=30s
}

# --- Deletion via the API removes the Docker object ---

@test "deleting Container resource removes Docker container" {
    run_e -0 docker create --name test-delete busybox sleep inf
    cid=${output}

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s

    rdd ctl delete container "${cid}" --namespace="${RDD_NAMESPACE}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s

    run -1 docker inspect test-delete
}

@test "deleting an in-use Image keeps the finalizer until the container is removed" {
    run_e -0 docker run -d --name test-inuse busybox sleep inf
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    # Resolve the Image mirror name from its repoTag.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=busybox:latest" -o name
    assert_output
    image_ref=${output}

    # Docker refuses to remove an image referenced by a running
    # container, so the mirror stays in Terminating with the finalizer
    # intact until the container is gone.
    rdd ctl delete "${image_ref}" --namespace="${RDD_NAMESPACE}" --wait=false

    # Both reads are race-free: deletionTimestamp lands synchronously
    # with the DELETE response, and the finalizer cannot be removed
    # while the container still references the image.
    run -0 rdd ctl get "${image_ref}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.deletionTimestamp}'
    assert_output
    run -0 rdd ctl get "${image_ref}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers[0]}'
    assert_output "engine.rancherdesktop.io/mirror"

    # Remove the container so Docker permits the image removal. The
    # next reconcile's finalizer retry succeeds and the Image mirror
    # is finally collected.
    docker rm --force test-inuse
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        "${image_ref}" --timeout=30s
}

# --- Container actions via annotation ---

# assert_action_consumed reports success once the reconciler has
# removed the action annotation, which is how we know the dispatch
# has completed.
assert_action_consumed() {
    local cid=$1
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o "jsonpath={.metadata.annotations['containers\.rancherdesktop\.io/action']}"
    refute_output
}

# request_action sets the action annotation and blocks until the
# reconciler removes it. The annotation is a one-shot trigger.
request_action() {
    local cid=$1 action=$2
    rdd ctl annotate container "${cid}" --namespace="${RDD_NAMESPACE}" --overwrite \
        "containers.rancherdesktop.io/action=${action}"
    try --max 30 --delay 1 -- assert_action_consumed "${cid}"
}

assert_last_action() {
    local cid=$1 action=$2 state=$3
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.action}={.status.lastAction.state}'
    assert_output "${action}=${state}"
}

@test "stop action stops a running container" {
    run_e -0 docker run -d --name test-state busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    request_action "${cid}" stop

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" stop Succeeded

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "exited"
}

@test "start action restarts a stopped container" {
    run -0 docker inspect test-state --format '{{.Id}}'
    cid=${output}

    request_action "${cid}" start

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" start Succeeded

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "running"
}

@test "pause and unpause actions toggle a running container" {
    run -0 docker inspect test-state --format '{{.Id}}'
    cid=${output}

    request_action "${cid}" pause
    rdd ctl wait --for=jsonpath='{.status.status}'=paused \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" pause Succeeded

    request_action "${cid}" unpause
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" unpause Succeeded

    run -0 docker inspect test-state --format '{{.State.Status}}'
    assert_output "running"
}

@test "pause action on a stopped container records failure" {
    # Docker refuses to pause a non-running container. The action
    # still removes the annotation; the failure surfaces in
    # status.lastAction so the GUI can react.
    run_e -0 docker run -d --name test-pause-fail busybox /bin/true
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    request_action "${cid}" pause
    assert_last_action "${cid}" pause Failed

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.error}'
    assert_output --partial "not running"

    docker rm --force test-pause-fail
}

@test "unpause action on a stopped container records failure" {
    # Docker's unpause would succeed silently on a non-running container
    # because the pre-check sees it as not-paused. The reconciler inspects
    # Running explicitly so unpause on a stopped container surfaces a
    # failure, symmetric with pause's behavior above.
    run_e -0 docker run -d --name test-unpause-fail busybox /bin/true
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    request_action "${cid}" unpause
    assert_last_action "${cid}" unpause Failed

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.error}'
    assert_output --partial "not running"

    docker rm --force test-unpause-fail
}

@test "invalid action annotation is rejected by the webhook" {
    # An unknown action value must fail admission so the reconciler never
    # sees a value it cannot process. Without this gate, a bad value would
    # fail to persist in status.lastAction (the CRD enum rejects it) and
    # the annotation would stay in place, wedging the retry loop.
    run_e -0 docker run -d --name test-invalid-action busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    run -1 rdd ctl annotate container "${cid}" --namespace="${RDD_NAMESPACE}" --overwrite \
        "containers.rancherdesktop.io/action=bogus"
    assert_output --partial "invalid"

    # The rejected request leaves status.lastAction untouched.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction}'
    refute_output

    docker rm --force test-invalid-action
}

@test "action annotation on create is rejected by the webhook" {
    # The engine watcher creates Container mirrors and never sets the
    # action annotation. Reject any hand-written create that carries one,
    # so it cannot drive a Docker action against its metadata.name.
    run -1 rdd ctl apply -f - <<EOF
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: Container
metadata:
  name: hand-written-create
  namespace: "${RDD_NAMESPACE}"
  annotations:
    containers.rancherdesktop.io/action: start
EOF
    assert_output --partial "not allowed on create"
}

@test "restart action restarts a running container" {
    run_e -0 docker run -d --name test-restart busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    # Record the pre-restart StartedAt so we can verify the container
    # was actually restarted rather than left untouched.
    run -0 docker inspect test-restart --format '{{.State.StartedAt}}'
    before=${output}

    request_action "${cid}" restart
    assert_last_action "${cid}" restart Succeeded

    run -0 docker inspect test-restart --format '{{.State.StartedAt}}'
    refute_output "${before}"

    docker rm --force test-restart
}

@test "lastAction survives a direct docker stop" {
    # lastAction records the most recent reconciler action and survives
    # observable state changes (e.g. a direct docker stop) that the
    # reconciler did not initiate.
    run_e -0 docker run -d --name test-persist busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    request_action "${cid}" start
    assert_last_action "${cid}" start Succeeded

    docker stop test-persist
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" start Succeeded

    docker rm --force test-persist
}

@test "lastAction timestamps advance across consecutive actions" {
    run_e -0 docker run -d --name test-timestamps busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    request_action "${cid}" stop
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s

    # Capture timestamps from the first action; both must be set.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.observedAt}'
    assert_output
    first_observed=${output}

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.completedAt}'
    assert_output
    first_completed=${output}

    # Wait so the second action's timestamps land in a later second.
    sleep 1

    request_action "${cid}" start
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=30s
    assert_last_action "${cid}" start Succeeded

    # Both timestamps must advance on the second action.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.observedAt}'
    refute_output "${first_observed}"

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.completedAt}'
    refute_output "${first_completed}"

    docker rm --force test-timestamps
}

# --- Cleanup on shutdown ---

@test "stopping VM removes all mirror resources" {
    # Make sure we have at least one resource to verify cleanup.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s

    rdd set running=false

    run -0 rdd ctl get containers --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get images --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get volumes --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

@test "rdd set running=false returns promptly when already stopped" {
    # rdd set waits for Settled=True with observedGeneration matching
    # the just-patched spec. When the VM is already stopped, the App
    # controller stamps Settled=True on the same reconcile pass that
    # applied the no-op patch, so the wait returns immediately.
    rdd set --timeout=10s running=false
}

@test "VM start recreates ContainerNamespace/moby after cleanup" {
    # The "stopping VM removes all mirror resources" test above swept
    # ContainerNamespace/moby along with the rest of the mirrors, so
    # restarting the VM must recreate it. This bridges the teardown
    # above to the ContainerNamespace delete test below, which needs
    # the namespace to exist before it can delete it.
    rdd set running=true
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/moby --timeout=10s
}

@test "deleting ContainerNamespace/moby completes without a finalizer hang" {
    # moby ContainerNamespace has no mirror finalizer, so a user delete
    # must return promptly rather than get trapped in Terminating.
    rdd ctl delete ContainerNamespace/moby --namespace="${RDD_NAMESPACE}" --timeout=10s
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

# --- containerd backend ---

@test "containerd backend reports ContainerEngineReady=NotApplicable and skips mirroring" {
    # Stop first so there is no stale True/Connected from moby to
    # satisfy the Settled wait below before the engine reconciler has
    # processed the containerd switch.
    rdd set running=false

    # Start with containerd. rdd set waits for Settled=True, which
    # requires ContainerEngineReady=True. The engine reconciler
    # satisfies that immediately with reason NotApplicable because
    # engine mirroring only supports the moby backend.
    rdd set containerEngine.name=containerd running=true

    run -0 rdd ctl get app app \
        -o jsonpath='{.status.conditions[?(@.type=="ContainerEngineReady")].reason}'
    assert_output "NotApplicable"

    # No mirror resources should exist in containerd mode.
    run -0 rdd ctl get containers --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get images --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get volumes --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

# --- Docker context management ---

# docker_context_dir returns the ~/.docker/contexts/meta/<hash> directory for
# the named context. Docker derives the sub-directory from sha256(name).
docker_context_dir() {
    local name="$1"
    local hash
    # sha256sum on Linux, shasum on macOS
    if command -v sha256sum &>/dev/null; then
        hash=$(printf '%s' "${name}" | sha256sum | awk '{print $1}')
    else
        hash=$(printf '%s' "${name}" | shasum -a 256 | awk '{print $1}')
    fi
    echo "${DOCKER_CONFIG}/contexts/meta/${hash}"
}

# assert_docker_context checks config.json's currentContext against the
# expected value; suitable for polling with try.
assert_docker_context() { # <expected-context>
    run -0 cat "${DOCKER_CONFIG}/config.json"
    run -0 jq_output '.currentContext'
    assert_output "$1"
}

@test "moby engine creates Docker context for the instance" {
    # Restart with moby (the containerd test above may have left the engine in
    # containerd mode).
    rdd set running=true containerEngine.name=moby
    rdd ctl wait --for=condition=ContainerEngineReady \
        app/app --timeout=30s

    meta_file="$(docker_context_dir "${CONTEXT_NAME}")/meta.json"

    assert_file_exists "${meta_file}"

    run -0 cat "${meta_file}"
    meta=${output}

    run -0 jq_raw '.Name' "${meta}"
    assert_output "${CONTEXT_NAME}"

    if is_windows; then
        run -0 jq_raw '.Endpoints.docker.Host' "${meta}"
        assert_output "npipe:////./pipe/docker_engine"
    else
        run -0 rdd service paths docker_socket
        socket_path=${output}
        run -0 jq_raw '.Endpoints.docker.Host' "${meta}"
        assert_output "unix://${socket_path}"
    fi
}

@test "rdd run targets the instance Docker context" {
    # rdd run sets DOCKER_CONTEXT to the instance and clears DOCKER_HOST
    # for the child. The suite exports DOCKER_HOST, and docker reports the
    # "default" context whenever DOCKER_HOST is set, so a named result also
    # proves the variable was cleared. The caller's currentContext in
    # config.json is left unchanged.
    run_e -0 rdd run docker context show
    assert_output "${CONTEXT_NAME}"
}

@test "rdd run propagates the child exit status without a spurious error line" {
    # A non-zero child exit returns a cliexit.Error carrying only a code; main
    # must not log it as a bare level=error line. run -7 asserts the status and
    # merges stderr into the output so refute_line can catch the spurious line.
    run -7 rdd run sh -c 'exit 7'
    refute_line --partial "level=error"
}

@test "moby engine sets currentContext when no healthy context exists" {
    # Seed a named context pointing at an unreachable socket and make it
    # current. This forces the reconciler into the probeNamedDockerContext
    # branch with a guaranteed-failing endpoint, independent of whether the
    # host has a running Docker daemon at the default socket (e.g.
    # ubuntu-latest runners ship Docker pre-installed). Using a named context
    # also avoids the stale-env issue: the service process's DOCKER_HOST is
    # frozen at first start, so FromEnv-based probing of "" / "default" is
    # host-dependent and unreliable from inside a long-lived daemon.
    probe_target="test-unreachable"
    run_e -0 docker_context_dir "${probe_target}"
    probe_dir="${output}"
    mkdir -p "${probe_dir}"
    cat >"${probe_dir}/meta.json" <<EOF
{"Name":"${probe_target}","Endpoints":{"docker":{"Host":"unix:///nonexistent/does-not-exist.sock","SkipTLSVerify":false}}}
EOF
    printf '{"currentContext":"%s"}\n' "${probe_target}" >"${DOCKER_CONFIG}/config.json"

    rdd set running=false
    rdd set running=true containerEngine.name=moby
    rdd ctl wait --for=condition=ContainerEngineReady app/app --timeout=30s

    # The probe goroutine runs asynchronously; give it a moment.
    try --max 6 --delay 1 -- assert_docker_context "${CONTEXT_NAME}"

    assert_docker_context "${CONTEXT_NAME}"
}

@test "stopping moby engine removes Docker context and clears currentContext" {
    rdd set running=false

    run_e -0 docker_context_dir "${CONTEXT_NAME}"
    context_dir="${output}"

    # removeDockerContext runs asynchronously after the reconciler processes
    # the Running=False transition; poll until the directory is gone.
    try --max 10 --delay 1 -- test ! -d "${context_dir}"
    assert_dir_not_exists "${context_dir}"

    # currentContext should either be gone or point to something else.
    run -0 cat "${DOCKER_CONFIG}/config.json"
    run -0 jq_output '.currentContext // empty'
    refute_output "${CONTEXT_NAME}"
}
