load '../../helpers/load'

# Port fallback testing to ensure RDD can run when ports are busy

# Track netcat PIDs for cleanup
declare -a NETCAT_PIDS=()

local_teardown() {
    kill "${NETCAT_PIDS[@]}" 2>/dev/null || :
}

# Extract port from kubeconfig server URL
get_kubeconfig_port() {
    rdd svc config | grep "server:" | sed 's/.*127\.0\.0\.1:\([0-9]*\).*/\1/'
}

# Check if a port is available on localhost
is_port_available() {
    local port="$1"
    ! netstat -an | grep -q "^tcp4.*127\.0\.0\.1\.$port "
}

# Test basic functionality without port conflicts
@test 'control plane starts on default port when available' {
    rdd svc delete
    rdd svc create --controllers="rdd"

    expected_port=$(get_expected_port 6443)

    # Skip test if the expected port is not available
    if ! is_port_available "$expected_port"; then
        skip "Port $expected_port is not available for testing"
    fi

    ARGS_JSON="${PATH_APP_HOME}/args.json"
    assert_file_exist "$ARGS_JSON"
    assert_file_contains "$ARGS_JSON" '"--secure-port"'
    assert_file_contains "$ARGS_JSON" "\"$expected_port\""

    rdd svc start

   # Verify kubeconfig uses the expected port
    run -0 get_kubeconfig_port
    assert_output "$expected_port"

    # Verify API works
    run -0 rdd ctl get namespaces
    assert_output --partial "default"
}

@test 'control plane falls back when desired port is busy' {
    rdd svc stop

    # Calculate expected port dynamically and occupy it
    expected_port=$(get_expected_port)
    nc -l 127.0.0.1 "$expected_port" &
    NETCAT_PIDS+=($!)

    # Give netcat time to bind
    sleep 1

    # Verify the port is actually occupied by checking netstat
    run -0 netstat -an
    assert_output --partial "127.0.0.1.$expected_port"

    # Start RDD - it should fall back to a different port
    rdd svc start

    run -0 get_kubeconfig_port
    refute_output "$expected_port"

    # Verify API works on the fallback port
    run -0 rdd ctl get namespaces
    assert_output --partial "default"

}

@test 'control plane finds random port when both desired and default ports are busy' {
    run -0 rdd svc stop

    # Calculate expected port dynamically
    expected_port=$(get_expected_port 6443)

    # Occupy both the desired port and default fallback (6443) on localhost
    nc -l 127.0.0.1 6443 &
    NETCAT_PIDS+=($!)
    nc -l 127.0.0.1 "$expected_port" &
    NETCAT_PIDS+=($!)

    # Give netcat time to bind
    sleep 2

    # Verify both ports are occupied by checking netstat
    run -0 netstat -an
    assert_output --partial "127.0.0.1.6443"
    run -0 netstat -an
    assert_output --partial "127.0.0.1.$expected_port"

    # Start RDD - it should fall back to a random available port
    rdd svc start

    # Get the port from kubeconfig
    run -0 get_kubeconfig_port
    refute_output "6443"
    refute_output "$expected_port"

    # Should be a high random port (typically > 1024)
    [[ $output -gt 1024 ]]

    # Verify API works on the random fallback port
    run -0 rdd ctl get namespaces
    assert_output --partial "default"

    # Verify RDD controllers are working
    run -0 rdd ctl get crd configmapreplicasets.rdd.rancherdesktop.io
    assert_output --partial "configmapreplicasets.rdd.rancherdesktop.io"

}

@test 'port override works with fallback mechanism' {
    run -0 rdd svc stop

    # Occupy port 7000 on localhost
    nc -l 127.0.0.1 7000 &
    NETCAT_PIDS+=($!)

    # Give netcat time to bind
    sleep 1

    # Try to start with --secure-port 7000 (should fall back)
    rdd svc start --secure-port 7000

    # Get the port from kubeconfig
    run -0 get_kubeconfig_port
    refute_output 7000

    # Verify API works on the fallback port
    run -0 rdd ctl get namespaces
    assert_output --partial "default"
}

@test 'rdd svc create accepts --secure-port and persists it' {
    rdd svc delete

    # Skip test if the expected port is not available
    if ! is_port_available 7777; then
        skip "Port 7777 is not available for testing"
    fi

    # Create with custom secure port
    rdd svc create --secure-port 7777 --controllers="rdd"

    # Verify the port is saved in args.json
    ARGS_JSON="${PATH_APP_HOME}/args.json"
    assert_file_exist "$ARGS_JSON"
    assert_file_contains "$ARGS_JSON" '"--secure-port"'
    assert_file_contains "$ARGS_JSON" '"7777"'

    # Start the service
    rdd svc start

    # Verify kubeconfig uses the specified port
    run -0 get_kubeconfig_port
    assert_output "7777"

    # Verify API works
    run -0 rdd ctl get namespaces
    assert_output --partial "default"

    # Stop and restart to verify persistence
    rdd svc stop
    rdd svc start

    # Verify port is still 7777
    run -0 get_kubeconfig_port
    assert_output "7777"
}
