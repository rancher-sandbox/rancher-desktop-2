# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors
# SPDX-FileCopyrightText: Copyright The Lima Authors

# The symlink mechanism is adapted from
# https://github.com/lima-vm/lima/blob/master/hack/bats/tests/yq.bats

load '../../helpers/load'

# link_rdd_as symlinks the rdd binary under <name> in the per-test temp dir, so
# invoking "${BATS_TEST_TMPDIR}/<name>" runs rdd as a multi-call binary.
link_rdd_as() { # <name>
    ln -sf "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${BATS_TEST_TMPDIR}/$1"
}

@test 'a kubectl symlink runs the embedded kubectl' {
    link_rdd_as "kubectl${EXE}"
    run -0 "${BATS_TEST_TMPDIR}/kubectl${EXE}" --help
    assert_line --partial 'kubectl controls the Kubernetes cluster manager.'
}

@test 'a yq symlink runs the embedded yq' {
    link_rdd_as "yq${EXE}"
    run -0 "${BATS_TEST_TMPDIR}/yq${EXE}" --version
    assert_output --regexp '^yq .*mikefarah.* version v'
}

@test 'a multi-call name with another extension is rejected' {
    # Only a Windows .exe extension is stripped, so yq.rdd is not yq.
    link_rdd_as "yq.rdd${EXE}"
    run -1 "${BATS_TEST_TMPDIR}/yq.rdd${EXE}" -n .foo=42
    assert_output --partial 'unsupported command name'
}

@test 'an unrecognized name is rejected' {
    link_rdd_as "notacommand${EXE}"
    run -1 "${BATS_TEST_TMPDIR}/notacommand${EXE}" --help
    assert_output --partial 'unsupported command name'
}
