# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# SPDX-FileCopyrightText: Copyright The Lima Authors

# Adapted from https://github.com/lima-vm/lima/blob/master/hack/bats/tests/yq.bats

load '../../helpers/load'

@test 'rdd yq subcommand reports version' {
    run -0 rdd yq --version
    assert_output --regexp '^yq .*mikefarah.* version v'
}

@test 'rdd yq evaluates expressions' {
    run -0 rdd yq -n .foo=42
    assert_output 'foo: 42'
}

@test 'rdd yq supports output format options' {
    run -0 rdd yq -n -o json -I 0 .foo=42
    assert_output '{"foo":42}'
}

@test 'rdd yq sets non-zero exit code on invalid input' {
    run -1 rdd yq -n foo
    assert_output --partial "invalid input"
}
