load '../../helpers/load'

assert_version() {
    # Matches git version tag format or commit hash
    assert_output --regexp '^(v[0-9]+\.[0-9]+\.[0-9]+|[a-f0-9]{7,})'
}

@test 'rdd version command displays RDD version information' {
    run -0 rdd version
    assert_version
}

@test 'rdd --version displays RDD version information' {
    run -0 rdd --version
    assert_version
}

@test 'rdd --version=true displays simple version information' {
    run -0 rdd --version=true
    assert_version
}

@test 'rdd --version=raw displays detailed version information' {
    run -0 rdd --version=raw
    assert_line --partial "Version:"
    assert_line --partial "GitCommit:"
    assert_line --partial "BuildDate:"
    assert_line --partial "GoVersion:"
    assert_line --partial "Compiler:"
    assert_line --partial "Platform:"
}

@test 'rdd --version=custom shows custom version' {
    run -0 rdd --version=v1.0.0-test
    assert_output "v1.0.0-test"
}

@test 'rdd --version and rdd version produce same output' {
    run -0 rdd --version
    local version_flag_output="${output}"

    run -0 rdd version
    assert_output "${version_flag_output}"
}
