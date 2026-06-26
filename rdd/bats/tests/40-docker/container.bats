# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Container lifecycle commands against the rdd Docker engine.

local_setup_file() {
    start_docker_engine
    # Pull once so each test times the command under test, not an image pull.
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

@test "run executes a command and streams its output" {
    run -0 ctrctl run --rm "${IMAGE_BUSYBOX}" echo hello
    assert_output hello
}

@test "run --detach starts a container that ps lists" {
    run_e -0 ctrctl run --detach --name rdd-run "${IMAGE_BUSYBOX}" sleep 300
    cid=${output}

    # ps prints the 12-character short ID, a prefix of the full ID from run.
    run -0 ctrctl ps --quiet --filter name=rdd-run
    assert_output "${cid:0:12}"

    ctrctl rm --force rdd-run
}

@test "exec runs a command inside a running container" {
    ctrctl run --detach --name rdd-exec "${IMAGE_BUSYBOX}" sleep 300
    run -0 ctrctl exec rdd-exec echo inside
    assert_output inside
    ctrctl rm --force rdd-exec
}

@test "logs returns a container's stdout" {
    # Run in the foreground so the container finishes before logs is read.
    ctrctl run --name rdd-logs "${IMAGE_BUSYBOX}" sh -c 'echo one; echo two'
    run -0 ctrctl logs rdd-logs
    assert_line --index 0 one
    assert_line --index 1 two
    ctrctl rm --force rdd-logs
}

@test "inspect reports container state" {
    ctrctl run --detach --name rdd-inspect "${IMAGE_BUSYBOX}" sleep 300
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-inspect
    assert_output running
    ctrctl rm --force rdd-inspect
}

@test "cp copies files between host and container" {
    ctrctl run --detach --name rdd-cp "${IMAGE_BUSYBOX}" sleep 300

    echo marker >"${BATS_TEST_TMPDIR}/in.txt"
    run -0 host_path "${BATS_TEST_TMPDIR}/in.txt"
    in_host=${output}
    run -0 host_path "${BATS_TEST_TMPDIR}/out.txt"
    out_host=${output}

    ctrctl cp "${in_host}" rdd-cp:/in.txt
    run -0 ctrctl exec rdd-cp cat /in.txt
    assert_output marker

    ctrctl cp rdd-cp:/in.txt "${out_host}"
    run -0 cat "${BATS_TEST_TMPDIR}/out.txt"
    assert_output marker

    ctrctl rm --force rdd-cp
}

@test "stop, start, and restart move a container through its states" {
    ctrctl run --detach --name rdd-lifecycle "${IMAGE_BUSYBOX}" sleep 300

    ctrctl stop --time 1 rdd-lifecycle
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-lifecycle
    assert_output exited

    ctrctl start rdd-lifecycle
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-lifecycle
    assert_output running

    ctrctl restart --time 1 rdd-lifecycle
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-lifecycle
    assert_output running

    ctrctl rm --force rdd-lifecycle
}

@test "pause and unpause freeze and resume a container" {
    ctrctl run --detach --name rdd-pause "${IMAGE_BUSYBOX}" sleep 300

    ctrctl pause rdd-pause
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-pause
    assert_output paused

    ctrctl unpause rdd-pause
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-pause
    assert_output running

    ctrctl rm --force rdd-pause
}

@test "kill stops a running container" {
    ctrctl run --detach --name rdd-kill "${IMAGE_BUSYBOX}" sleep 300
    ctrctl kill rdd-kill
    run -0 ctrctl inspect --format '{{.State.Status}}' rdd-kill
    assert_output exited
    ctrctl rm --force rdd-kill
}

@test "wait blocks until a container exits and reports its code" {
    ctrctl run --detach --name rdd-wait "${IMAGE_BUSYBOX}" sh -c 'exit 3'
    run -0 ctrctl wait rdd-wait
    assert_output 3
    ctrctl rm --force rdd-wait
}

@test "rename changes a container's name" {
    ctrctl run --detach --name rdd-old "${IMAGE_BUSYBOX}" sleep 300
    ctrctl rename rdd-old rdd-new

    run -0 ctrctl ps --quiet --filter name=rdd-new
    assert_output
    run -0 ctrctl ps --quiet --filter name=rdd-old
    refute_output

    ctrctl rm --force rdd-new
}

@test "stats reports a running container without streaming" {
    ctrctl run --detach --name rdd-stats "${IMAGE_BUSYBOX}" sleep 300
    run -0 ctrctl stats --no-stream --format '{{.Name}}' rdd-stats
    assert_output rdd-stats
    ctrctl rm --force rdd-stats
}

@test "port lists a container's published port mappings" {
    ctrctl run --detach --name rdd-port --publish 127.0.0.1::80 "${IMAGE_BUSYBOX}" sleep 300
    run -0 ctrctl port rdd-port 80
    assert_output --partial 127.0.0.1:
    ctrctl rm --force rdd-port
}

@test "exec on a stopped container fails" {
    ctrctl run --detach --name rdd-stopped "${IMAGE_BUSYBOX}" sh -c 'exit 0'
    ctrctl wait rdd-stopped
    run ctrctl exec rdd-stopped true
    assert_failure
    ctrctl rm --force rdd-stopped
}
