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
    # The resources under test are in the App namespace.
    RDD_NAMESPACE=${RDD_APP_NAMESPACE}
    export RDD_NAMESPACE
}

local_teardown() {
    # No suite setup cleans the host home directory.
    if [[ -n "${host_fixture_dir:-}" ]]; then
        rm -rf "${host_fixture_dir}"
    fi
}

# Create host_fixture_dir with "file" in it, under the home directory because
# the guest sees it. host_fixture_src is that directory as host_path prints it.
create_host_fixture() {
    run -0 mktemp -d "${HOME}/rdd-bats-nerdctl.XXXXXX"
    host_fixture_dir=${output}
    create_file "${host_fixture_dir}/file" <<<shared
    run -0 host_path "${host_fixture_dir}"
    host_fixture_src=${output}
}

# Print where the guest sees a host path, /mnt/<drive>/... on Windows.
guest_path() { # <host-path>
    if is_windows; then
        local path
        path=$(cygpath -m "$1")
        local drive=${path:0:1}
        printf '/mnt/%s%s\n' "${drive,,}" "${path:2}"
    else
        printf '%s\n' "$1"
    fi
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
# reading the mode through it also proves the /run/containerd directory is
# traversable. 666 is the permissions drop-in's chmod; containerd itself
# creates the socket root-only.
assert_containerd_socket_open() {
    run -0 rdd limavm shell "${VM_NAME}" stat --format=%a /run/containerd/containerd.sock
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

# --- Host paths ---

@test "nerdctl bind-mounts a host directory" {
    # The guest sees the host home directory through Lima's mount, or on
    # Windows through WSL's /mnt/<drive> automount after the stub rewrites
    # the path.
    create_host_fixture
    run_e -0 nerdctl run --rm --volume "${host_fixture_src}:/data" busybox cat /data/file
    assert_output shared
}

@test "nerdctl passes a named volume through" {
    # A volume name must reach nerdctl unrewritten; rewritten as a path, it
    # would bind-mount a directory instead of creating the volume.
    nerdctl run --rm --volume rdd-bats-vol:/data busybox true
    nerdctl volume inspect rdd-bats-vol
    nerdctl volume rm rdd-bats-vol
}

@test "nerdctl bind-mounts a host directory given as a backslash path" {
    skip_on_unix
    create_host_fixture
    run -0 cygpath -w "${host_fixture_dir}"
    backslash_dir=${output}
    run_e -0 nerdctl run --rm --volume "${backslash_dir}:/data" busybox cat /data/file
    assert_output shared
}

@test "nerdctl bind-mounts the working directory" {
    create_host_fixture
    cd "${host_fixture_dir}"
    run_e -0 nerdctl run --rm --volume .:/data busybox cat /data/file
    assert_output shared
}

@test "nerdctl bind-mounts a rooted path on the working directory's drive" {
    skip_on_unix
    create_host_fixture
    cd "${host_fixture_dir}"
    run -0 cygpath -w "${host_fixture_dir}"
    rooted=${output:2}
    run_e -0 nerdctl run --rm --volume "${rooted}:/data" busybox cat /data/file
    assert_output shared
}

@test "nerdctl keeps a bind mount's mode" {
    create_host_fixture
    run -1 nerdctl run --rm --volume "${host_fixture_src}:/data:ro" busybox touch /data/x
    assert_output --partial "Read-only file system"
    assert_file_not_exists "${host_fixture_dir}/x"
}

@test "nerdctl bind-mounts a host directory given with --mount" {
    create_host_fixture
    run_e -0 nerdctl run --rm --mount "type=bind,source=${host_fixture_src},target=/data" busybox cat /data/file
    assert_output shared
}

@test "nerdctl passes an anonymous volume through" {
    run_e -0 nerdctl run --rm --volume /data busybox ls -d /data
    assert_output /data
}

@test "nerdctl rejects a drive path without a container path" {
    skip_on_unix
    create_host_fixture
    cd "${host_fixture_dir}"
    run -1 nerdctl run --rm --volume C:/data busybox true
    assert_output --partial "invalid volume mount C:/data: expected source:container-path"
    # Passed through, C:/data would make nerdctl bind-mount a "C" directory,
    # which it creates in the working directory.
    assert_not_exists "${host_fixture_dir}/C"
}

@test "nerdctl reads env files from the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/env" <<<GREETING=hi
    create_file "${host_fixture_dir}/env2" <<<NAME=bats
    run_e -0 nerdctl run --rm --env-file "${host_fixture_src}/env" busybox printenv GREETING
    assert_output hi
    run_e -0 nerdctl run --rm --env-file "${host_fixture_src}/env,${host_fixture_src}/env2" busybox printenv GREETING NAME
    assert_output $'hi\nbats'
}

@test "nerdctl reads a label file relative to the working directory" {
    create_host_fixture
    create_file "${host_fixture_dir}/labels" <<<com.example.test=yes
    cd "${host_fixture_dir}"
    nerdctl run --name rdd-bats-labels --label-file labels busybox true
    run_e -0 nerdctl container inspect --format '{{index .Config.Labels "com.example.test"}}' rdd-bats-labels
    assert_output yes
    nerdctl rm rdd-bats-labels
}

@test "nerdctl writes a cidfile on the host" {
    create_host_fixture
    nerdctl run --rm --cidfile "${host_fixture_src}/cid" busybox true
    run -0 cat "${host_fixture_dir}/cid"
    assert_output --regexp '^[0-9a-f]{64}$'
}

@test "nerdctl exec reads an env file from the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/env" <<<GREETING=hi
    nerdctl run --detach --name rdd-bats-exec busybox sleep 300
    run_e -0 nerdctl exec --env-file "${host_fixture_src}/env" rdd-bats-exec printenv GREETING
    assert_output hi
    nerdctl rm --force rdd-bats-exec
}

@test "nerdctl saves and loads an image through a host file" {
    create_host_fixture
    nerdctl image save --output "${host_fixture_src}/busybox.tar" busybox
    assert_file_not_empty "${host_fixture_dir}/busybox.tar"
    run_e -0 nerdctl image load --input "${host_fixture_src}/busybox.tar"
    assert_output --partial "Loaded image"
}

@test "nerdctl compose resolves paths against a compose file in another directory" {
    create_host_fixture
    create_file "${host_fixture_dir}/compose.yaml" <<'EOF'
services:
  hello:
    image: busybox
    volumes:
      - ./file:/data/file
    command: cat /data/file
EOF
    cd "${HOME}"
    run -0 guest_path "${host_fixture_dir}"
    guest_dir=${output}
    run_e -0 nerdctl compose --file "${host_fixture_src}/compose.yaml" config
    assert_line --partial "source: ${guest_dir}/file"
    # compose run allocates a TTY whenever --interactive is on, and a bats run
    # does not have a terminal. Without a TTY, compose prefixes the output.
    run_e -0 nerdctl compose --file "${host_fixture_src}/compose.yaml" run --rm --interactive=false hello
    assert_line --regexp 'shared$'
    nerdctl compose --file "${host_fixture_src}/compose.yaml" down
}

@test "nerdctl builds a context directory on the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY file /file
EOF
    nerdctl build --tag rdd-bats-build "${host_fixture_src}"
    run_e -0 nerdctl run --rm rdd-bats-build cat /file
    assert_output shared
    nerdctl rmi rdd-bats-build
}

@test "nerdctl builds a context given as a backslash path" {
    skip_on_unix
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY file /file
EOF
    run -0 cygpath -w "${host_fixture_dir}"
    backslash_dir=${output}
    nerdctl build --tag rdd-bats-build-backslash "${backslash_dir}"
    run_e -0 nerdctl run --rm rdd-bats-build-backslash cat /file
    assert_output shared
    nerdctl rmi rdd-bats-build-backslash
}

@test "nerdctl builds the working directory" {
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY file /file
EOF
    cd "${host_fixture_dir}"
    nerdctl build --tag rdd-bats-build-cwd .
    run_e -0 nerdctl run --rm rdd-bats-build-cwd cat /file
    assert_output shared
    nerdctl rmi rdd-bats-build-cwd
}

@test "nerdctl builds with a Dockerfile outside the context" {
    create_host_fixture
    create_file "${host_fixture_dir}/context/file" <<<shared
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY file /file
EOF
    nerdctl build --tag rdd-bats-build-file --file "${host_fixture_src}/Dockerfile" \
        "${host_fixture_src}/context"
    run_e -0 nerdctl run --rm rdd-bats-build-file cat /file
    assert_output shared
    nerdctl rmi rdd-bats-build-file
}

@test "nerdctl build writes an iidfile on the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<<'FROM busybox'
    nerdctl build --tag rdd-bats-build-iid --iidfile "${host_fixture_src}/iid" "${host_fixture_src}"
    run -0 cat "${host_fixture_dir}/iid"
    assert_output --regexp '^sha256:[0-9a-f]{64}$'
    nerdctl rmi rdd-bats-build-iid
}

# On Linux the guest's mount of the home directory refuses the lchown that
# buildctl, running as root, makes on every file it exports (#1129).
skip_local_export_on_linux() {
    if is_linux; then
        skip "the guest's home mount refuses lchown from buildctl (#1129)"
    fi
}

@test "nerdctl build exports a local output to the host" {
    skip_local_export_on_linux
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM scratch
COPY file /file
EOF
    nerdctl build --output "type=local,dest=${host_fixture_src}/out" "${host_fixture_src}"
    run -0 cat "${host_fixture_dir}/out/file"
    assert_output shared
}

@test "nerdctl build exports to a host directory given without a type" {
    # nerdctl wraps a bare --output value as type=local,dest=<value>.
    skip_local_export_on_linux
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM scratch
COPY file /file
EOF
    nerdctl build --output "${host_fixture_src}/out" "${host_fixture_src}"
    run -0 cat "${host_fixture_dir}/out/file"
    assert_output shared
}

@test "nerdctl build writes and reads a local cache on the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY file /file
EOF
    nerdctl build --tag rdd-bats-build-cache \
        --cache-to "type=local,dest=${host_fixture_src}/cache" "${host_fixture_src}"
    assert_file_exists "${host_fixture_dir}/cache/index.json"
    # buildkit only warns when it cannot read the --cache-from directory, and
    # its local cache from the first build marks the steps CACHED anyway. Only
    # the import line shows that it read the host directory.
    run -0 nerdctl build --progress=plain --tag rdd-bats-build-cache \
        --cache-from "type=local,src=${host_fixture_src}/cache" "${host_fixture_src}"
    assert_output --partial "importing cache manifest from"
    nerdctl rmi rdd-bats-build-cache
}

@test "nerdctl build reads a secret from the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
RUN --mount=type=secret,id=rdd-bats cp /run/secrets/rdd-bats /secret
EOF
    nerdctl build --tag rdd-bats-build-secret \
        --secret "id=rdd-bats,src=${host_fixture_src}/file" "${host_fixture_src}"
    run_e -0 nerdctl run --rm rdd-bats-build-secret cat /secret
    assert_output shared
    nerdctl rmi rdd-bats-build-secret
}

@test "nerdctl build reads a named build context from the host" {
    create_host_fixture
    create_file "${host_fixture_dir}/named/file" <<<named
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
COPY --from=extra file /file
EOF
    nerdctl build --tag rdd-bats-build-context \
        --build-context "extra=${host_fixture_src}/named" "${host_fixture_src}"
    run_e -0 nerdctl run --rm rdd-bats-build-context cat /file
    assert_output named
    nerdctl rmi rdd-bats-build-context
}

@test "nerdctl build forwards an ssh key file from the host" {
    # buildctl reads the key file in the guest, so the build fails when the
    # guest cannot reach the path.
    create_host_fixture
    ssh-keygen -q -t ed25519 -N '' -f "${host_fixture_dir}/key"
    create_file "${host_fixture_dir}/Dockerfile" <<'EOF'
FROM busybox
RUN --mount=type=ssh,id=rdd-bats,target=/ssh.sock test -S /ssh.sock
EOF
    nerdctl build --tag rdd-bats-build-ssh \
        --ssh "rdd-bats=${host_fixture_src}/key" "${host_fixture_src}"
    nerdctl rmi rdd-bats-build-ssh
}

@test "nerdctl cp copies a host file into a container" {
    create_host_fixture
    nerdctl run --detach --name rdd-bats-cp-in busybox sleep 300
    nerdctl cp "${host_fixture_src}/file" rdd-bats-cp-in:/file
    run_e -0 nerdctl exec rdd-bats-cp-in cat /file
    assert_output shared
    nerdctl rm --force rdd-bats-cp-in
}

@test "nerdctl cp copies a container file to the host" {
    create_host_fixture
    nerdctl run --detach --name rdd-bats-cp-out busybox sleep 300
    nerdctl exec rdd-bats-cp-out sh -c 'echo inside >/file'
    nerdctl cp rdd-bats-cp-out:/file "${host_fixture_src}/out"
    run -0 cat "${host_fixture_dir}/out"
    assert_output inside
    nerdctl rm --force rdd-bats-cp-out
}

@test "nerdctl cp copies a host path relative to the working directory" {
    create_host_fixture
    cd "${host_fixture_dir}"
    nerdctl run --detach --name rdd-bats-cp-cwd busybox sleep 300
    nerdctl cp file rdd-bats-cp-cwd:/file
    run_e -0 nerdctl exec rdd-bats-cp-cwd cat /file
    assert_output shared
    nerdctl rm --force rdd-bats-cp-cwd
}

@test "nerdctl compose cp copies to and from a one-letter service" {
    # On Windows c:/file could name drive C: or the service c. When the other
    # argument is a host path, the stub rewrites only that one, and nerdctl
    # reads c:/file as the service.
    create_host_fixture
    create_file "${host_fixture_dir}/compose.yaml" <<'EOF'
services:
  c:
    image: busybox
    command: sleep 300
EOF
    cd "${host_fixture_dir}"
    nerdctl compose up --detach
    nerdctl compose cp ./file c:/file
    nerdctl compose cp c:/file ./back
    run -0 cat "${host_fixture_dir}/back"
    assert_output shared
    nerdctl compose down
}

@test "nerdctl compose cp takes a backslash path as the host side" {
    skip_on_unix
    create_host_fixture
    create_file "${host_fixture_dir}/compose.yaml" <<'EOF'
services:
  c:
    image: busybox
    command: sleep 300
EOF
    cd "${host_fixture_dir}"
    run -0 cygpath -w "${host_fixture_dir}"
    backslash_file=${output}\\file
    nerdctl compose up --detach
    nerdctl compose cp "${backslash_file}" c:/file
    nerdctl compose cp c:/file ./back
    run -0 cat "${host_fixture_dir}/back"
    assert_output shared
    nerdctl compose down
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
