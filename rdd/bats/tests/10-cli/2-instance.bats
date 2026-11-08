load '../../helpers/load'

local_setup_file() {
    # MSYS2's ln -s makes a copy, which selects the instance by name too.
    ln -s "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${BATS_FILE_TMPDIR}/rdd-vampire${EXE}"
}

@test 'Default instance is 2' {
    unset RDD_INSTANCE
    run -0 rdd svc status
    assert_line --partial "rancher-desktop-2"
}

@test 'RDD_INSTANCE overrides default instance' {
    [[ ${RDD_INSTANCE} != "2" ]]
    run -0 rdd svc status
    assert_line --partial "rancher-desktop-${RDD_INSTANCE}"
    refute_line --partial "rancher-desktop-2"
}

@test '--instance overrides RDD_INSTANCE' {
    [[ ${RDD_INSTANCE} != "vampire" ]]
    run -0 rdd --instance vampire svc status
    assert_line --partial "rancher-desktop-vampire"
    refute_line --partial "rancher-desktop-${RDD_INSTANCE}"
}

@test '--instance=name overrides RDD_INSTANCE' {
    run -0 rdd --instance=vampire svc status
    assert_line --partial "rancher-desktop-vampire"
    refute_line --partial "rancher-desktop-${RDD_INSTANCE}"
}

@test '--instance= is ignored and RDD_INSTANCE is used' {
    run -0 rdd --instance= svc status
    assert_line --partial "rancher-desktop-${RDD_INSTANCE}"
    refute_line --partial "rancher-desktop-2"
}

@test 'rdd-<instance> overrides RDD_INSTANCE' {
    run -0 "${BATS_FILE_TMPDIR}/rdd-vampire${EXE}" svc status
    assert_line --partial "rancher-desktop-vampire"
    refute_line --partial "rancher-desktop-${RDD_INSTANCE}"
}

@test '--instance overrides rdd-<instance>' {
    run -0 "${BATS_FILE_TMPDIR}/rdd-vampire${EXE}" --instance werewolf svc status
    assert_line --partial "rancher-desktop-werewolf"
    refute_line --partial "rancher-desktop-vampire"
}

@test '--instance only works directly after the rdd command name and not after svc' {
    run -1 rdd svc --instance vampire status
    assert_line --partial "unknown flag: --instance"
}

@test '--instance only works directly after the rdd command name and not after svc status' {
    run -1 rdd svc status --instance vampire
    assert_line --partial "unknown flag: --instance"
}

@test '--instance with an invalid name is rejected' {
    run -1 rdd --instance DEV version
    assert_output --partial "invalid instance name"
}
