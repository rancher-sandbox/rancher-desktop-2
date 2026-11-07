load '../../helpers/load'

# Prove that a control plane started from an rdd-<instance> copy serves the
# instance that --instance selects. svc start runs the copy again as its
# service serve child, and that child must not take its instance from the
# copy's name. The test uses a copy because on Linux os.Executable() resolves
# a symlink back to bin/rdd.

local_setup_file() {
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${BATS_FILE_TMPDIR}/rdd-vampire${EXE}"
}

# named_rdd runs the rdd-vampire copy. It closes BATS fds 3 and 4 so the daemon
# that it spawns cannot inherit them and hang bats, matching the rdd() helper.
named_rdd() {
    "${BATS_FILE_TMPDIR}/rdd-vampire${EXE}" "$@" 3>&- 4>&-
}

@test 'service started from rdd-<instance> serves the --instance instance' {
    rdd svc delete
    rdd svc create
    # svc start waits for this instance's API server, so it fails if the child
    # came up as instance vampire.
    named_rdd --instance "${RDD_INSTANCE}" svc start
    named_rdd --instance "${RDD_INSTANCE}" svc stop
}
