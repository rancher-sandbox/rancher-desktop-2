load '../../helpers/load'

# TODO: test `rdd svc start --wait=false`

@test 'Make sure instance does not exist' {
    rdd svc delete || :
    assert_dir_not_exist "${PATH_APP_HOME}"
}

@test 'Delete instance succeeds even when the instance does not exist' {
    run -0 rdd svc delete
}

@test 'verify instance does not exist yet' {
    run -0 rdd svc status
    run -0 extract_msg
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been created: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been started: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane PID is: 0"
}

@test 'create instance' {
    run -0 rdd svc create
    run -0 extract_msg
    assert_output "successfully created \"rancher-desktop-${RDD_INSTANCE}\" control plane"
    assert_dir_exist "${PATH_APP_HOME}"
}

@test 'verify instance does exist but has not been started' {
    run -0 rdd svc status
    run -0 extract_msg
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been created: true"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been started: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane PID is: 0"
}

@test 'start instance' {
    run -0 rdd svc start
    run -0 extract_msg
    assert_output <<EOT
successfully started "rancher-desktop-${RDD_INSTANCE}" control plane
waiting for "rancher-desktop-${RDD_INSTANCE}" control plane to be ready
EOT
}

@test 'verify instance has been started' {
    run -0 rdd svc status
    run -0 extract_msg
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been created: true"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been started: true"
    refute_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane PID is: 0"
}

@test 'logs shows control plane stderr' {
    run -0 rdd svc logs
    assert_output --partial "apiserver"
}

@test 'logs --stdout shows control plane stdout' {
    # stdout may be empty, just verify the command succeeds
    rdd svc logs --stdout
}

@test 'stop instance' {
    run -0 rdd svc stop
    run -0 extract_msg
    assert_output "successfully stopped \"rancher-desktop-${RDD_INSTANCE}\" control plane"
    assert_dir_exist "${PATH_APP_HOME}"
}

@test 'verify instance has been stopped' {
    run -0 rdd svc status
    run -0 extract_msg
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been created: true"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been started: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane PID is: 0"
}

@test 'Delete instance' {
    run -0 rdd svc delete
    run -0 extract_msg
    assert_output "successfully deleted \"rancher-desktop-${RDD_INSTANCE}\" control plane"
    assert_dir_not_exist "${PATH_APP_HOME}"
}

@test 'verify instance has been deleted' {
    run -0 rdd svc status
    run -0 extract_msg
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been created: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane has been started: false"
    assert_line "\"rancher-desktop-${RDD_INSTANCE}\" control plane PID is: 0"
}
