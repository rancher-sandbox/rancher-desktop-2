load '../../helpers/load'

ALL_KEYS="args_file config dir lima_home log_dir pid_file short_dir tls_dir"

@test 'rdd svc paths prints all keys in table format' {
    run -0 rdd svc paths
    for key in ${ALL_KEYS}; do
        assert_line --regexp "^${key} "
    done
}

@test 'rdd svc paths --output=json produces valid JSON with all keys' {
    run -0 rdd svc paths --output=json
    local json="${output}"
    for key in ${ALL_KEYS}; do
        jq --exit-status --arg k "${key}" 'has($k)' <<<"${json}"
    done
}

@test 'rdd svc paths --output=shell produces shell export statements' {
    run -0 rdd svc paths --output=shell
    for key in ${ALL_KEYS}; do
        assert_line --regexp "^export RDD_${key^^}="
    done
}

@test 'rdd svc paths <key> prints only the value' {
    run -0 rdd svc paths log_dir
    # Output should be a single line with no key prefix
    assert_output --regexp '^(/|[A-Z]:)'
    refute_output --regexp '^log_dir'
    assert_equal "${#lines[@]}" 1
}

@test 'rdd svc paths with invalid key fails and lists valid keys' {
    run -1 rdd svc paths no_such_key
    assert_output --partial 'unknown key'
    assert_output --partial 'no_such_key'
    assert_output --partial 'valid keys'
}
