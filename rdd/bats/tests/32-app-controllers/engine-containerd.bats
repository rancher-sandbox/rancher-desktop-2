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
    RDD_NAMESPACE=$(rdd ctl get app app -o jsonpath='{.spec.namespace}')
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
    ((output > 0))
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
