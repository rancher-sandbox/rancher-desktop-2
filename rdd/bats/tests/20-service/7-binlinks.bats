load '../../helpers/load'

# Prove the daemon publishes the application's bundled binaries into
# ~/.rd<instance>/bin only when rdd runs from inside the app bundle. A fake
# bundle holds a real rdd copy at .../<resources>/<os>/bin/rdd next to a couple
# of stand-in tool files; the daemon links whatever sits beside that rdd.
# Symlinks need privileges on Windows, so the daemon falls back to hardlinks;
# RDD_NO_SYMLINKS forces that fallback for the last test.

local_setup_file() {
    # Clean up from any previous run; svc delete also removes the short dir.
    rdd svc delete || :

    # Mock up a packaged Rancher Desktop distribution so rdd thinks it is
    # bundled. The siblings are stand-ins; only their names matter. macOS
    # capitalizes Resources; Linux and Windows use lowercase.
    resources=resources
    is_macos && resources=Resources
    fake_bin="${BATS_FILE_TMPDIR}/Rancher Desktop.app/Contents/${resources}/${OS}/bin"
    mkdir -p "${fake_bin}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${fake_bin}/rdd${EXE}"
    echo "stand-in docker" >"${fake_bin}/docker${EXE}"
    echo "stand-in helm" >"${fake_bin}/helm${EXE}"

    FAKE_BIN="${fake_bin}"
    DEST_DIR="${RDD_SHORT_DIR}/bin"
    save_var FAKE_BIN DEST_DIR
}

local_setup() {
    # Each test starts the daemon from a chosen location, so stop any daemon a
    # previous test left running; an already-running instance rejects the start.
    rdd svc stop || :
}

# fake_rdd runs the bundled rdd copy. It closes BATS fds 3 and 4 so the daemon
# it spawns cannot inherit them and hang bats, matching the rdd() helper. A
# daemon fake_rdd starts must be stopped with fake_rdd too: on Windows rdd only
# recognizes a control plane running its own binary, so the repo rdd in
# local_setup cannot stop the bundled copy's daemon, and would orphan it.
fake_rdd() {
    "${FAKE_BIN}/rdd${EXE}" "$@" 3>&- 4>&-
}

# assert_hardlink_to asserts that the link shares the target's file and is not a
# symlink, as the hardlink fallback produces.
assert_hardlink_to() { # <target> <link>
    assert_file_exist "$2"
    refute [ -L "$2" ]
    assert [ "$2" -ef "$1" ]
}

@test 'publishes bundled binaries when started from the app bundle' {
    load_var FAKE_BIN DEST_DIR
    fake_rdd svc start
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
    assert_symlink_to "${FAKE_BIN}/helm${EXE}" "${DEST_DIR}/helm${EXE}"
    # kubectl and nerdctl are not bundled; they link to rdd.
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl${EXE}"
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/nerdctl${EXE}"
    # Stop the bundled daemon with fake_rdd; the repo rdd cannot (see fake_rdd).
    fake_rdd svc stop
}

@test 'leaves working links untouched when started standalone' {
    load_var FAKE_BIN DEST_DIR
    # The bundle run's links still resolve, so a standalone rdd leaves its own
    # rdd and kubectl links alone and never touches docker or helm.
    rdd svc start
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
    assert_symlink_to "${FAKE_BIN}/helm${EXE}" "${DEST_DIR}/helm${EXE}"
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl${EXE}"
}

@test 'updates the links when the bundle changes and rdd runs from it again' {
    load_var FAKE_BIN DEST_DIR
    # Add a tool and drop one, then restart from the bundle. The added tool
    # shares the nerdctl multicall name; the bundled binary must win.
    echo "stand-in nerdctl" >"${FAKE_BIN}/nerdctl${EXE}"
    rm "${FAKE_BIN}/helm${EXE}"
    fake_rdd svc start
    assert_symlink_to "${FAKE_BIN}/nerdctl${EXE}" "${DEST_DIR}/nerdctl${EXE}"
    # The dropped tool's link is gone, proving the directory was recreated.
    assert_link_not_exist "${DEST_DIR}/helm${EXE}"
    # Unchanged entries are still linked.
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl${EXE}"
    fake_rdd svc stop
}

@test 'repairs missing or dangling rdd and kubectl links when started standalone' {
    load_var FAKE_BIN DEST_DIR
    # Simulate an instance whose app was deleted: an rdd run from a throwaway
    # directory links rdd and kubectl to itself, then removing that directory
    # leaves the links dangling. Driving this through rdd avoids depending on
    # how the shell creates symlinks, which differs under MSYS2.
    rm -f "${DEST_DIR}/rdd${EXE}" "${DEST_DIR}/kubectl${EXE}"
    throwaway="${BATS_TEST_TMPDIR}/throwaway"
    mkdir -p "${throwaway}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${throwaway}/rdd${EXE}"
    "${throwaway}/rdd${EXE}" svc start 3>&- 4>&-
    "${throwaway}/rdd${EXE}" svc stop 3>&- 4>&-
    rm -rf "${throwaway}"
    # The standalone rdd repairs its own links to point at the running binary.
    rdd svc start
    standalone="${PATH_REPO_ROOT}/bin/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/kubectl${EXE}"
    # The bundle's nerdctl link still resolves, so it survives the repair.
    assert_symlink_to "${FAKE_BIN}/nerdctl${EXE}" "${DEST_DIR}/nerdctl${EXE}"
    # The unrelated docker link from the bundle run is left in place.
    assert_symlink_to "${FAKE_BIN}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
}

@test 'prunes dangling tool links left after an uninstall' {
    load_var DEST_DIR
    # Publish from a throwaway bundle, then delete it so its links dangle — the
    # uninstall case. Driving setup through rdd gives real symlinks the daemon
    # recognizes (MSYS2 ln -s would not).
    resources=resources
    is_macos && resources=Resources
    bundle="${BATS_TEST_TMPDIR}/Gone.app/Contents/${resources}/${OS}/bin"
    mkdir -p "${bundle}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${bundle}/rdd${EXE}"
    echo "stand-in docker" >"${bundle}/docker${EXE}"
    "${bundle}/rdd${EXE}" svc start 3>&- 4>&-
    "${bundle}/rdd${EXE}" svc stop 3>&- 4>&-
    assert_symlink_to "${bundle}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
    rm -rf "${bundle}"
    # A standalone rdd prunes the now-dangling docker link so it cannot shadow a
    # tool on PATH, and repairs its own rdd and kubectl links to itself.
    rdd svc start
    assert_link_not_exist "${DEST_DIR}/docker${EXE}"
    standalone="${PATH_REPO_ROOT}/bin/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/kubectl${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/nerdctl${EXE}"
}

@test 'publishes hardlinks when symlinks are disabled' {
    load_var FAKE_BIN DEST_DIR
    # RDD_NO_SYMLINKS forces the hardlink fallback that Windows hits without
    # developer mode, so the same publish runs on a system that lacks symlinks.
    export RDD_NO_SYMLINKS=1
    fake_rdd svc start
    assert_hardlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_hardlink_to "${FAKE_BIN}/docker${EXE}" "${DEST_DIR}/docker${EXE}"
    # kubectl is not bundled; it hardlinks to rdd.
    assert_hardlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl${EXE}"
    fake_rdd svc stop
}
