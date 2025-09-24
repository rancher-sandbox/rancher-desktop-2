# bats file_tags=opensuse

load '../helpers/load'

local_setup() {
    output=1
}

@test '1 >= 0' {
    assert_output_ge 0
}

@test '1 >= 1' {
    assert_output_ge 1
}

@test '1 not >= 2' {
    run assert_output_ge 2
    assert_failure
}

@test 'zero not >= 2' {
    output="zero"
    run assert_output_ge 2
    assert_failure
}

@test '1 < 2' {
    assert_output_lt 2
}

@test '1 not < 1' {
    run assert_output_lt 1
    assert_failure
}

@test 'zero not < 1' {
    output="zero"
    run assert_output_lt 1
    assert_failure
}
