load '../../helpers/load'

# Prove the daemon publishes the application's bundled binaries into
# ~/.rd<instance>/bin only when rdd runs from inside the app bundle. A fake
# bundle holds a real rdd copy at .../<resources>/<os>/bin/rdd next to a couple
# of stand-in tool files; the daemon links whatever sits beside that rdd.

local_setup_file() {
    skip_on_windows "Bundled binaries are only published on macOS and Linux."
    # Clean up from any previous run; svc delete also removes the short dir.
    rdd svc delete || :

    # Mock up a packaged Rancher Desktop distribution so rdd thinks it is
    # bundled. The siblings are stand-ins; only their names matter. macOS
    # capitalizes Resources; Linux uses lowercase.
    resources=Resources
    is_linux && resources=resources
    fake_bin="${BATS_FILE_TMPDIR}/Rancher Desktop.app/Contents/${resources}/${OS}/bin"
    mkdir -p "${fake_bin}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${fake_bin}/rdd${EXE}"
    echo "stand-in docker" >"${fake_bin}/docker"
    echo "stand-in helm" >"${fake_bin}/helm"

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
# it spawns cannot inherit them and hang bats, matching the rdd() helper.
fake_rdd() {
    "${FAKE_BIN}/rdd${EXE}" "$@" 3>&- 4>&-
}

@test 'publishes bundled binaries when started from the app bundle' {
    load_var FAKE_BIN DEST_DIR
    fake_rdd svc start
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker" "${DEST_DIR}/docker"
    assert_symlink_to "${FAKE_BIN}/helm" "${DEST_DIR}/helm"
    # kubectl is not bundled; it links to rdd.
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl"
}

@test 'leaves working links untouched when started standalone' {
    load_var FAKE_BIN DEST_DIR
    # The bundle run's links still resolve, so a standalone rdd leaves its own
    # rdd and kubectl links alone and never touches docker or helm.
    rdd svc start
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker" "${DEST_DIR}/docker"
    assert_symlink_to "${FAKE_BIN}/helm" "${DEST_DIR}/helm"
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl"
}

@test 'updates the links when the bundle changes and rdd runs from it again' {
    load_var FAKE_BIN DEST_DIR
    # Add a tool and drop one, then restart from the bundle.
    echo "stand-in nerdctl" >"${FAKE_BIN}/nerdctl"
    rm "${FAKE_BIN}/helm"
    fake_rdd svc start
    assert_symlink_to "${FAKE_BIN}/nerdctl" "${DEST_DIR}/nerdctl"
    # The dropped tool's link is gone, proving the directory was recreated.
    assert_link_not_exist "${DEST_DIR}/helm"
    # Unchanged entries are still linked.
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${FAKE_BIN}/docker" "${DEST_DIR}/docker"
    assert_symlink_to "${FAKE_BIN}/rdd${EXE}" "${DEST_DIR}/kubectl"
}

@test 'repairs missing or dangling rdd and kubectl links when started standalone' {
    load_var FAKE_BIN DEST_DIR
    # Simulate an instance whose app was deleted: rdd's link dangles, kubectl is
    # gone, and an unrelated tool link still resolves.
    rm -f "${DEST_DIR}/rdd${EXE}" "${DEST_DIR}/kubectl"
    ln -s "${DEST_DIR}/deleted/rdd${EXE}" "${DEST_DIR}/rdd${EXE}"
    rdd svc start
    # The standalone rdd repairs its own links to point at the running binary.
    standalone="${PATH_REPO_ROOT}/bin/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/rdd${EXE}"
    assert_symlink_to "${standalone}" "${DEST_DIR}/kubectl"
    # The unrelated docker link from the bundle run is left in place.
    assert_symlink_to "${FAKE_BIN}/docker" "${DEST_DIR}/docker"
}
