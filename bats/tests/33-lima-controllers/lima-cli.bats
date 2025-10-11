load '../../helpers/load'

# LimaVM CLI tests - tests the rdd lima subcommand for managing LimaVM resources.

local_setup_file() {
    setup_rdd_control_plane "lima"
    rdd ctl create namespace "lima-test-ns"
}

assert_running() {
    local state=$1
    run -0 rdd ctl get limavm test-vm -n lima-test-ns -o jsonpath='{.spec.running}'
    assert_output "$state"
}

@test "lima create/start/stop/delete basic workflow" {
    # Create VM
    run -0 rdd limavm create test-vm -n lima-test-ns
    assert_output --partial 'created'
    assert_running "false"

    # Start the VM (cross-namespace lookup: no -n flag needed)
    run -0 rdd limavm start test-vm
    assert_output --partial 'started'
    assert_running "true"

    # Stop the VM
    run -0 rdd limavm stop test-vm
    assert_output --partial 'stopped'
    assert_running "false"

    # Delete the VM
    run -0 rdd limavm delete test-vm
    assert_output --partial 'deleted'
    run -1 rdd ctl get limavm test-vm -n lima-test-ns
    assert_output --partial "not found"
}

@test "lima commands fail gracefully when VM does not exist" {
    run -1 rdd limavm start nonexistent
    assert_output --partial 'not found in any namespace'

    run -1 rdd limavm stop nonexistent
    assert_output --partial 'not found in any namespace'

    run -1 rdd limavm delete nonexistent
    assert_output --partial 'not found in any namespace'
}

@test "lima help text is displayed" {
    # lima is an alias of the limavm command
    run -0 rdd lima --help
    assert_output --partial "LimaVM virtual machines"
    assert_output --partial "Create a new LimaVM"
    assert_output --partial "Start a LimaVM"
    assert_output --partial "Stop a LimaVM"
    assert_output --partial "Delete a LimaVM"
}
