load '../../helpers/load'

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

@test '--instance only works directly after the rdd command name and not after svc' {
    run -1 rdd svc --instance vampire status
    assert_line --partial "unknown flag: --instance"
}

@test '--instance only works directly after the rdd command name and not after svc status' {
    run -1 rdd svc status --instance vampire
    assert_line --partial "unknown flag: --instance"
}
