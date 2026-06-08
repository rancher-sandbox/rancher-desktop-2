load '../../helpers/load'

# Assert `rdd svc stop --wait` and `rdd svc delete` exit with code 4
# (cliexit.CodeTimeout) when --timeout expires. A 1ms deadline fires before
# the graceful-shutdown loop observes the service exit, so the wait sends
# SIGTERM (or TerminateProcess on Windows) and returns
# context.DeadlineExceeded wrapped in cliexit.Timeout. svc delete also
# preserves the instance directory on timeout (see docs/design/cmd_service.md)
# so the user can retry.

local_setup_file() {
    rdd svc delete --timeout=120s
    rdd svc create
    rdd svc start
}

@test 'svc stop --timeout=1ms exits with code 4' {
    run -4 rdd svc stop --wait --timeout=1ms
    assert_output --partial 'context deadline exceeded'
}

@test 'svc delete --timeout=1ms exits with code 4 and preserves the instance directory' {
    # Hard reset: the prior test sent SIGTERM but does not wait for the
    # process to exit, so a plain `rdd svc start` could still see
    # Running()=true and take the already-running branch.
    rdd svc delete --timeout=10s || :
    rdd svc create
    rdd svc start
    run -4 rdd svc delete --timeout=1ms
    assert_output --partial 'context deadline exceeded'
    run -0 rdd svc status
    assert_output --partial 'has been created: true'
}

@test 'svc start --timeout=1ms exits with code 4' {
    # Reset to a known state; prior tests left the control plane half-dead.
    rdd svc delete --timeout=120s
    rdd svc create
    run -4 rdd svc start --wait --timeout=1ms
    assert_output --partial 'context deadline exceeded'
}
