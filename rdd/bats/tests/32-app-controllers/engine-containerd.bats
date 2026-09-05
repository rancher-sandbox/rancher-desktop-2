# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Containerd engine tests — verify that the engine controller mirrors
# containerd namespaces and containers into ContainerNamespace and
# Container resources. Tests build on each other in file order: the
# lifecycle tests share the mirror-smoke container.

VM_NAME="rd"

local_setup_file() {
    if is_windows; then
        skip "containerd mirroring is not supported on Windows yet"
    fi
    rdd svc delete
    rdd set containerEngine.name=containerd running=true
    # Mirror resources live in App.spec.namespace. Override RDD_NAMESPACE
    # to whatever the App was created with so the test queries the same
    # namespace the engine controller uses, regardless of CRD defaults.
    run -0 rdd ctl get app app -o jsonpath='{.spec.namespace}'
    RDD_NAMESPACE=${output}
    export RDD_NAMESPACE
}

# nerdctl runs the given command inside the VM; host-side nerdctl wiring
# is out of scope in v1. sudo is required because non-root nerdctl
# insists on rootless mode, even with an explicit --address.
nerdctl() {
    rdd limavm shell "${VM_NAME}" sudo nerdctl \
        --address /run/k3s/containerd/containerd.sock "$@"
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
    # is removed directly — no untag re-inspection like Docker needs.
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
    # must still request a terminal; a container whose OCI spec asks for one
    # cannot be created without it.
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

@test "stop action honors the container stop signal and timeout" {
    # The container ignores SIGTERM and exits 0 on SIGUSR1, so it can only
    # stop promptly with the recorded signal. A ten-second stop means the
    # signal was ignored and the SIGKILL escalation ended it.
    run_e -0 nerdctl run --detach --name signal-actions --stop-signal SIGUSR1 \
        --stop-timeout 30 busybox \
        sh -c 'trap "exit 0" USR1; trap "" TERM; while :; do sleep 1; done'
    cid=${output}
    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    request_action "${cid}" stop
    assert_last_action "${cid}" stop Succeeded

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.exitCode}'
    assert_output 0

    nerdctl rm --force signal-actions
}

# --- Names that are not valid object names ---

@test "a containerd namespace that is not a valid object name gets no mirror" {
    # containerd namespace names are freer than Kubernetes object names, so
    # the mirror is skipped; the containers inside it are still mirrored,
    # which is what makes the skip safe.
    nerdctl namespace create Not_Valid
    run_e -0 nerdctl --namespace Not_Valid run --detach --name hidden-ns \
        busybox sleep inf
    cid=${output}

    rdd ctl wait --for=jsonpath='{.status.status}'=running \
        --namespace="${RDD_NAMESPACE}" container/"${cid}" --timeout=60s

    run -0 rdd ctl get container "${cid}" --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.status.namespace}'
    assert_output "Not_Valid"

    run -0 rdd ctl get containernamespaces --namespace="${RDD_NAMESPACE}" \
        -o jsonpath='{.items[*].metadata.name}'
    refute_output --partial "Not_Valid"

    # A namespace only removes once it holds nothing, and the run above
    # pulled busybox into it.
    nerdctl --namespace Not_Valid rm --force hidden-ns
    nerdctl --namespace Not_Valid rmi --force busybox
    nerdctl namespace remove Not_Valid
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
