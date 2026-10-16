# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Containerd engine tests: verify that the engine controller mirrors
# containerd namespaces, containers and images into ContainerNamespace,
# Container and Image resources, drives container actions, and forwards
# mirror deletes to containerd. Tests build on each other in file order.

VM_NAME="rd"

local_setup_file() {
    rdd svc delete
    rdd set containerEngine.name=containerd running=true
    # Mirror resources live in App.spec.namespace. Override RDD_NAMESPACE
    # to whatever the App was created with so the test queries the same
    # namespace the engine controller uses, regardless of CRD defaults.
    run -0 rdd ctl get app app -o jsonpath='{.spec.namespace}'
    RDD_NAMESPACE=${output}
    export RDD_NAMESPACE
}

@test "containerd engine reports ContainerEngineReady with reason Connected" {
    rdd ctl wait --for=condition=ContainerEngineReady app/app --timeout=60s
    run -0 rdd ctl get app app \
        -o jsonpath='{.status.conditions[?(@.type=="ContainerEngineReady")].reason}'
    assert_output "Connected"
}

@test "containerd engine reports namespace support" {
    run -0 rdd ctl get app app -o jsonpath='{.status.supportsNamespaces}'
    assert_output "true"
}

# limavm shell runs as the same unprivileged user as Lima's SSH forward, so
# reading the mode through it also proves the /run/k3s directories are
# traversable. 666 is the permissions drop-in's chmod; containerd itself
# creates the socket root-only.
assert_containerd_socket_open() {
    run -0 rdd limavm shell "${VM_NAME}" stat --format=%a /run/k3s/containerd/containerd.sock
    assert_output 666
}

@test "containerd socket is forwarded to the host" {
    if is_windows; then
        skip "Windows serves a named pipe, which curl cannot open"
    fi
    # Wait for containerd to create the socket and the drop-in to open it up.
    try --max 10 --delay 3 -- assert_containerd_socket_open

    run -0 rdd svc paths containerd_socket
    socket_path=${output}
    assert_exists "${socket_path}"

    # containerd's gRPC server answers a plain-HTTP client with an HTTP/2
    # GOAWAY frame; --http0.9 lets curl accept those raw bytes and exit 0.
    # A broken forward or unreachable guest socket exits nonzero instead.
    curl --unix-socket "${socket_path}" --http0.9 --max-time 5 --silent \
        --output /dev/null http://localhost/
}

@test "running a container creates a Container mirror" {
    run_e -0 nerdctl run --detach --name mirror-smoke busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.name} {.status.namespace}'
    assert_output "mirror-smoke default"
}

@test "ContainerNamespace mirror exists for the default namespace" {
    # The default namespace exists only once something was created in it;
    # the mirror-smoke container above guarantees that.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/default --timeout=30s
}

@test "stopping the container updates the mirror status" {
    run_e -0 nerdctl inspect --format '{{.Id}}' mirror-smoke
    cid=${output}

    nerdctl stop mirror-smoke

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
}

@test "removing the container removes the mirror" {
    run_e -0 nerdctl inspect --format '{{.Id}}' mirror-smoke
    cid=${output}

    nerdctl rm mirror-smoke

    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=30s
}

# --- Image mirroring ---

@test "pulled image has an Image mirror" {
    # busybox was pulled by the container tests above. containerd records
    # store full references, unlike Docker's short form.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" \
        --timeout=30s

    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" \
        -o jsonpath='{.items[0].status.namespace}'
    assert_output "default"

    # A zero size would mean the content-store walk silently failed.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" \
        -o jsonpath='{.items[0].status.size}'
    assert [ "${output}" -gt 0 ]
}

@test "tagging an image creates a second Image mirror" {
    nerdctl tag busybox:latest busybox:mirror-alias

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=docker.io/library/busybox:mirror-alias" \
        --timeout=30s

    # The original tag keeps its own mirror.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" -o name
    assert_output
}

@test "removing a tag removes only its Image mirror" {
    # containerd's ImageDelete event carries the record name, so the mirror
    # is removed directly, with no untag re-inspection like Docker needs.
    # wait --for=delete passes immediately for a mirror that never existed,
    # so prove this one is there before removing the tag.
    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:mirror-alias" -o name
    assert_output

    nerdctl rmi busybox:mirror-alias

    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=docker.io/library/busybox:mirror-alias" \
        --timeout=30s

    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" -o name
    assert_output
}

# --- Container mirror detail ---

@test "container mirror reports the command line" {
    run_e -0 nerdctl run --detach --name mirror-detail --publish 18080:80 \
        busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    # Path and args come from the OCI runtime spec, split the way Docker
    # reports them.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.path} {.status.args[0]}'
    assert_output "sleep inf"
}

@test "container mirror reports published ports" {
    run_e -0 nerdctl inspect --format '{{.Id}}' mirror-detail
    cid=${output}

    # containerd knows of no published ports; the mapping is nerdctl's.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.ports[0].name} {.status.ports[0].bindings[0].hostPort}'
    assert_output "80/tcp 18080"
}

@test "container mirror reports a start time" {
    run_e -0 nerdctl inspect --format '{{.Id}}' mirror-detail
    cid=${output}

    # containerd exposes no start time; this one was recorded from the
    # TaskStart event the watcher observed.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.startedAt}'
    assert_output
}

@test "container status.image joins against the Image mirror" {
    run_e -0 nerdctl inspect --format '{{.Id}}' mirror-detail
    cid=${output}

    # The UI looks the image mirror up by this value, so it has to be the
    # image ID rather than the reference containerd records.
    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.image}'
    image_id=${output}

    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:latest" \
        -o jsonpath='{.items[0].status.id}'
    assert_output "${image_id}"

    nerdctl rm --force mirror-detail
}

# --- Namespace lifecycle ---

@test "creating a containerd namespace creates its mirror" {
    # An empty namespace has nothing to sync, so the mirror can only come
    # from the namespace event itself.
    nerdctl namespace create mirror-ns

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/mirror-ns --timeout=30s
}

@test "removing a containerd namespace removes its mirror" {
    # wait --for=delete passes immediately for an object that never existed,
    # so prove the mirror is there before removing the namespace.
    rdd ctl get --namespace="${RDD_NAMESPACE}" ContainerNamespace/mirror-ns

    nerdctl namespace remove mirror-ns

    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/mirror-ns --timeout=30s
}

@test "unsupported namespace names are encoded" {
    local namespace_name=Not_A_Valid_Kubernetes_Namespace encoded_name
    nerdctl namespace create "${namespace_name}" \
        --label hello=world

    run_e -0 nerdctl --namespace "${namespace_name}" run --detach --name hidden-ns \
        busybox sleep inf
    cid=${output}

    run -0 sha256 "${namespace_name}"
    encoded_name=cns-${output}

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/"${encoded_name}" --timeout=30s

    # Status should be set
    wait_for_resource_status "ContainerNamespace" "${encoded_name}" name "${namespace_name}"
    wait_for_resource_status "ContainerNamespace" "${encoded_name}" labels.hello "world"

    # Updating labels should be reflected in the status
    nerdctl namespace update --label hello=foo "${namespace_name}"
    wait_for_resource_status "ContainerNamespace" "${encoded_name}" labels.hello "foo"

    # The container should be created
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_resource_status "Container" "${cid}" namespace "${namespace_name}"

    # Deleting the namespace should reap the object
    nerdctl --namespace "${namespace_name}" rm --force hidden-ns
    nerdctl --namespace "${namespace_name}" rmi --force busybox
    nerdctl namespace remove "${namespace_name}"

    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/"${encoded_name}" --timeout=30s
}

# --- Container actions via annotation ---
# The tests below share the test-actions container and build on each
# other in file order.

@test "stop action stops a running container" {
    run_e -0 nerdctl run --detach --name test-actions busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    request_action "${cid}" stop

    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" stop Succeeded

    run_e -0 nerdctl inspect --format '{{.State.Status}}' test-actions
    assert_output "exited"
}

@test "start action restarts a stopped container" {
    # Restarting an exited container recreates the task, including the
    # nerdctl log driver recorded in the container's log-uri label.
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    request_action "${cid}" start

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" start Succeeded

    run_e -0 nerdctl inspect --format '{{.State.Status}}' test-actions
    assert_output "running"
}

@test "pause and unpause actions toggle a running container" {
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    request_action "${cid}" pause
    rdd ctl wait --for=jsonpath='{.status.status}'=paused \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" pause Succeeded

    request_action "${cid}" unpause
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" unpause Succeeded
}

@test "pause action on a stopped container records failure" {
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    nerdctl stop test-actions
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    request_action "${cid}" pause
    assert_last_action "${cid}" pause Failed

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.lastAction.error}'
    assert_output --partial "not running"
}

@test "unpause action on a stopped container records failure" {
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    request_action "${cid}" unpause
    assert_last_action "${cid}" unpause Failed
}

@test "restart action starts a stopped container" {
    # containerd tasks cannot be restarted in place; the dispatch deletes
    # the exited task and creates a fresh one, matching Docker's behavior
    # of restart also starting stopped containers.
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    request_action "${cid}" restart

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" restart Succeeded
}

assert_container_pid_changed() { # <container> <previous-pid>
    run -0 get_resource_status container "$1" pid
    assert_output
    refute_output "$2"
}

@test "restart action restarts a running container" {
    # The stopped-container case above never reaches the signal-and-wait path,
    # because stopTask returns early for a task that is already Stopped.
    # Restarting a running one replaces the task, so the mirror reports a
    # different pid afterwards.
    run_e -0 nerdctl run --detach --name restart-actions busybox sleep inf
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    run -0 get_resource_status container "${cid}" pid
    assert_output
    before=${output}

    request_action "${cid}" restart
    assert_last_action "${cid}" restart Succeeded

    try --max 30 --delay 2 -- assert_container_pid_changed "${cid}" "${before}"

    nerdctl rm --force restart-actions
}

@test "lastAction survives a direct nerdctl stop" {
    # lastAction records the most recent reconciler action and must
    # survive status re-applies triggered by engine-side state changes
    # the reconciler did not initiate.
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}

    request_action "${cid}" start
    assert_last_action "${cid}" start Succeeded

    nerdctl stop test-actions
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s
    assert_last_action "${cid}" start Succeeded
}

@test "start action restarts a container that allocates a TTY" {
    # The task is recreated from the container record, so the recreated IO
    # must still request a terminal, or the runtime refuses the task.
    run_e -0 nerdctl run --detach --tty --name tty-actions busybox sleep inf
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    nerdctl stop tty-actions
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    request_action "${cid}" start
    assert_last_action "${cid}" start Succeeded
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    nerdctl rm --force tty-actions
}

@test "stop action honors the container stop signal" {
    # The container ignores SIGTERM and exits 0 on SIGUSR1, so the exit code
    # says which signal it got: an ignored one leaves the grace period to
    # expire and the SIGKILL escalation reports 137.
    # --stop-timeout keeps the label-reading path in play; nothing here
    # measures the timeout, since the container exits as soon as it is
    # signalled.
    run_e -0 nerdctl run --detach --name signal-actions --stop-signal SIGUSR1 \
        --stop-timeout 30 busybox \
        sh -c 'trap "exit 0" USR1; trap "" TERM; while :; do sleep 1; done'
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    request_action "${cid}" stop
    assert_last_action "${cid}" stop Succeeded

    # The action path writes lastAction alone; exitCode arrives with the
    # TaskExit sync. A running task reports exit code 0 too, so the mirror
    # has to reach exited before that field means anything.
    rdd ctl wait --for=jsonpath='{.status.status}'=exited \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.exitCode}'
    assert_output 0

    nerdctl rm --force signal-actions
}

# --- Finalizer-forwarded deletes ---

@test "deleting Container resource removes the containerd container" {
    # Delete while running so the finalizer path has to kill the task
    # before removing the container.
    nerdctl start test-actions
    run_e -0 nerdctl inspect --format '{{.Id}}' test-actions
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    rdd ctl delete container "${cid}" --namespace="${RDD_NAMESPACE}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        container/"${cid}" --timeout=60s

    run_e -1 nerdctl inspect test-actions
}

@test "deleting Image mirror removes the containerd image" {
    nerdctl tag busybox:latest busybox:delete-me
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=docker.io/library/busybox:delete-me" \
        --timeout=30s

    run -0 rdd ctl get image --namespace="${RDD_NAMESPACE}" \
        --field-selector "status.repoTag=docker.io/library/busybox:delete-me" -o name
    assert_output
    image_ref=${output}

    rdd ctl delete "${image_ref}" --namespace="${RDD_NAMESPACE}"
    rdd ctl wait --for=delete --namespace="${RDD_NAMESPACE}" \
        "${image_ref}" --timeout=60s

    run_e -1 nerdctl image inspect busybox:delete-me
}

# --- Cleanup on VM stop ---
# These run last: they stop and restart the VM, which sweeps every mirror.

@test "stopping VM removes all mirror resources" {
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/default --timeout=10s

    rdd set running=false

    run -0 rdd ctl get containers --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get images --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
    run -0 rdd ctl get ContainerNamespaces --namespace="${RDD_NAMESPACE}" --output=name
    refute_output
}

@test "VM start recreates the ContainerNamespace mirror after cleanup" {
    # The sweep above removed ContainerNamespace/default, so the full sync
    # on restart has to bring it back; the busybox image left in containerd
    # keeps the namespace alive across the restart.
    rdd set running=true

    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" \
        ContainerNamespace/default --timeout=60s
}
