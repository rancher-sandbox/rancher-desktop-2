load '../../helpers/load'

# A CLI waiting for the control plane must fail as soon as the service exits,
# instead of waiting out its 90s timeout. The test freezes serve with SIGSTOP
# so it cannot become ready while it still counts as running, then kills it.

local_setup_file() {
    # MSYS2's kill signals only Cygwin processes and cannot stop a native one.
    if is_windows; then
        skip "cannot SIGSTOP a native Windows process"
    fi
    rdd svc delete --timeout=120s
    rdd svc create
}

@test 'waiting CLI fails fast when the control plane exits before it is ready' {
    run -0 rdd svc paths pid_file
    pid_file=${output}

    rdd svc start --wait=false
    try --max 50 --delay 0.2 test -s "${pid_file}"
    run -0 cat "${pid_file}"
    pid=${output}
    kill -STOP "${pid}"

    # Kill serve only once the waiting CLI has seen it running. A CLI that
    # doesn't find serve running starts a new one instead.
    cli_log=${BATS_TEST_TMPDIR}/start.log
    rdd --log-level=info svc start >"${cli_log}" 2>&1 3>&- &
    cli_pid=$!
    try --max 100 --delay 0.2 assert_cli_waiting "${cli_log}"

    SECONDS=0
    kill -KILL "${pid}"
    cli_status=0
    wait "${cli_pid}" || cli_status=$?
    assert_equal "${cli_status}" 1
    run -0 echo "${SECONDS}"
    assert_output_lt 30
    run -0 cat "${cli_log}"
    assert_output --partial 'control plane exited before it became ready'
}

assert_cli_waiting() { # <log file>
    run -0 cat "$1"
    assert_output --partial 'control plane to be ready'
}
