load '../../helpers/load'

assert_klog_level() {
    local level=$1
    run -0 jq --compact-output . "$PATH_APP_HOME/args.json"
    assert_line --partial "\"-v\",\"$level\""
}

# Test log level validation
@test 'invalid log level shows error' {
    run -1 rdd --log-level=invalid svc delete
    assert_output --partial 'not a valid logrus Level: \"invalid\"'
}

@test 'valid log levels work: error' {
    # `rdd svc delete` will always succeed and only emits "info" level messages.
    run -0 rdd --log-level=error svc delete
    refute_output
}

@test 'valid log levels work: warn' {
    run -0 rdd --log-level=warn svc delete
    refute_output
}

@test 'valid log levels work: warning' {
    run -0 rdd --log-level warning svc delete
    refute_output
}

@test 'valid log levels work: info' {
    run -0 rdd --log-level=info svc delete
    assert_line --partial "control plane does not exist"
}

@test 'valid log levels work: debug' {
    run -0 rdd --log-level=debug svc delete
    assert_line --partial "control plane does not exist"
}

@test 'valid log levels work: trace' {
    run -0 rdd --log-level=trace svc delete
    assert_line --partial "control plane does not exist"
}

# Test log level persistence in service configuration
@test 'debug log level creates service with -v 1' {
    rdd svc delete
    run -0 rdd --log-level=debug svc create --controllers=""
    assert_klog_level 1
}

@test 'trace log level creates service with -v 2' {
    rdd svc delete
    run -0 rdd --log-level=trace svc create --controllers=""
    assert_klog_level 2
}

@test 'error log level creates service with -v 0' {
    run -0 rdd svc delete
    run -0 rdd --log-level=error svc create --controllers=""
    assert_klog_level 0
}

# Test default log levels based on developer mode
@test 'default log level in developer mode creates -v 1' {
    run -0 rdd svc delete
    # Developer mode should default to debug level
    run -0 env RDD_DEVELOPER_MODE=1 rdd svc create --controllers=""
    assert_klog_level 1
}

@test 'default log level in non-developer mode creates -v 0' {
    run -0 rdd svc delete
    # Non-developer mode should default to warning level
    run -0 env RDD_DEVELOPER_MODE=0 rdd svc create --controllers=""
    assert_klog_level 0
}

# Test start command override behavior
@test 'start command can override log level for session' {
    run -0 rdd svc delete
    # Create with error level
    run -0 rdd --log-level=error svc create --controllers=""
    assert_klog_level 0

    # Start with trace level override (this doesn't change persistent args)
    run -0 rdd --log-level=trace svc start
    assert_klog_level 0
}
