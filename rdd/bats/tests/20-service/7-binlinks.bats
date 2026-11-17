load '../../helpers/load'

# Prove the daemon publishes the application's bundled binaries into
# ~/.rd<instance>/bin only when rdd runs from inside the app bundle, or, in
# developer mode, from a checkout. A fake bundle holds a real rdd copy at
# .../<resources>/<os>/bin/rdd next to a couple of stand-in tool files; the
# daemon links whatever sits beside that rdd. A fake checkout similarly holds a
# real rdd copy at .../rdd/bin/rdd, with stand-in tools in a sibling
# resources/<os>/bin instead. Either source's docker CLI plugins (in a sibling
# docker-cli-plugins directory) are mirrored into both the bin directory and a
# plugins-only directory.
#
# The publish differs by platform. macOS and Linux get a symlink per tool (or a
# hardlink where symlinks need privileges; RDD_NO_SYMLINKS forces that fallback
# for the last test). Windows gets a forwarder per tool: a hardlink to a shared
# rdd-forwarder.exe, beside a <tool>.shim naming the real binary. The bundle
# must therefore carry rdd-forwarder.exe; a stand-in is enough here because these
# tests check the published link structure, not that the forwarder runs.
#
# The tests exercising genuinely standalone behavior (no bundle, no checkout)
# force RDD_DEVELOPER_MODE=0 in local_setup, because we may be running in
# developer mode, which will try to pick up files from a checkout instead.

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
    # Windows publishes forwarders the daemon copies from the bundled
    # rdd-forwarder.exe. Use the real binary the BATS job builds, so the
    # "forwarders run the bundled tool" test exercises its argv[0] passthrough;
    # the link-structure checks only need a file of that name.
    is_windows && cp "${PATH_REPO_ROOT}/bin/rdd-forwarder${EXE}" "${fake_bin}/rdd-forwarder${EXE}"

    # The bundle ships docker CLI plugins in a docker-cli-plugins directory
    # beside bin. The daemon mirrors them into the instance bin directory (so
    # they work as standalone commands) and into a plugins-only directory that
    # the path-management controller registers in docker's cliPluginsExtraDirs.
    plugin_src="${BATS_FILE_TMPDIR}/Rancher Desktop.app/Contents/${resources}/${OS}/docker-cli-plugins"
    mkdir -p "${plugin_src}"
    echo "stand-in buildx" >"${plugin_src}/docker-buildx${EXE}"
    echo "stand-in compose" >"${plugin_src}/docker-compose${EXE}"

    FAKE_BIN="${fake_bin}"
    FAKE_PLUGIN_SRC="${plugin_src}"
    save_var FAKE_BIN FAKE_PLUGIN_SRC

    # Mock up a checkout for developer mode's "running from a checkout" path.
    # It holds a real rdd copy at .../rdd/bin/rdd and stand-in tools in a
    # sibling resources/<os>/bin, the way `yarn`'s postinstall assembles them.
    # To force that code path, checkout_rdd turns developer mode on through
    # RDD_DEVELOPER_MODE.
    checkout_root="${BATS_FILE_TMPDIR}/checkout"
    checkout_bin="${checkout_root}/rdd/bin"
    mkdir -p "${checkout_bin}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${checkout_bin}/rdd${EXE}"
    is_windows && cp "${PATH_REPO_ROOT}/bin/rdd-forwarder${EXE}" "${checkout_bin}/rdd-forwarder${EXE}"

    checkout_tools="${checkout_root}/resources/${OS}/bin"
    mkdir -p "${checkout_tools}"
    create_file "${checkout_tools}/docker${EXE}" <<<"stand-in docker"
    create_file "${checkout_tools}/helm${EXE}" <<<"stand-in helm"

    checkout_plugin_src="${checkout_root}/resources/${OS}/docker-cli-plugins"
    mkdir -p "${checkout_plugin_src}"
    create_file "${checkout_plugin_src}/docker-buildx${EXE}" <<<"stand-in buildx"
    create_file "${checkout_plugin_src}/docker-compose${EXE}" <<<"stand-in compose"

    CHECKOUT_BIN="${checkout_bin}"
    CHECKOUT_TOOLS="${checkout_tools}"
    CHECKOUT_PLUGIN_SRC="${checkout_plugin_src}"
    save_var CHECKOUT_BIN CHECKOUT_TOOLS CHECKOUT_PLUGIN_SRC
}

local_setup() {
    # Each test starts the daemon from a chosen location, so stop any daemon a
    # previous test left running; an already-running instance rejects the start.
    rdd svc stop || :

    # The tests in this file that want genuinely standalone behavior (no
    # bundle, no checkout) run the real built rdd directly
    # (${PATH_REPO_ROOT}/bin/rdd), which happens to be exactly the layout
    # developer mode's own checkout heuristic looks for
    # (../cmd/rdd/main.go next to the bin directory, which a checkout always
    # has). Force developer mode off by default so those tests keep exercising
    # the standalone path; checkout_rdd overrides this back on.
    export RDD_DEVELOPER_MODE=0
}

# fake_rdd runs the bundled rdd copy. It closes BATS fds 3 and 4 so the daemon
# it spawns cannot inherit them and hang bats, matching the rdd() helper.
fake_rdd() {
    "${FAKE_BIN}/rdd${EXE}" "$@" 3>&- 4>&-
}

# checkout_rdd runs the checkout rdd copy with developer mode forced on. It
# closes BATS fds 3 and 4 so the daemon it spawns cannot inherit them and hang
# bats, matching the rdd() helper.
checkout_rdd() {
    RDD_DEVELOPER_MODE=1 "${CHECKOUT_BIN}/rdd${EXE}" "$@" 3>&- 4>&-
}

# assert_hardlink_to asserts that the link shares the target's file and is not a
# symlink, as the hardlink fallback produces.
assert_hardlink_to() { # <target> <link>
    assert_file_exist "$2"
    refute [ -L "$2" ]
    assert [ "$2" -ef "$1" ]
}

# assert_forwarder_to asserts the Windows forwarder published for <name>: a
# hardlink to the shared rdd-forwarder, not a symlink, beside a <name>.shim that
# names <target>. The shim's exact format is covered by the Go unit tests, so we
# only check it names the target binary (which also proves kubectl forwards to
# rdd, not to itself).
assert_forwarder_to() { # <target> <name>
    local dest="${RDD_BIN_DIR}/$2"
    assert_file_exist "${dest}"
    refute [ -L "${dest}" ]
    assert [ "${dest}" -ef "${RDD_BIN_DIR}/rdd-forwarder${EXE}" ]
    local shim="${RDD_BIN_DIR}/${2%"${EXE}"}.shim"
    assert_file_exist "${shim}"
    assert_file_contains "${shim}" "$(basename "$1")"
}

# assert_published asserts the bundle published <name> resolving to <target>: a
# symlink on macOS and Linux, a forwarder on Windows.
assert_published() { # <target> <name>
    if is_windows; then
        assert_forwarder_to "$1" "$2"
    else
        assert_symlink_to "$1" "${RDD_BIN_DIR}/$2"
    fi
}

# assert_published_in asserts <name> published in <dir> resolving to <target>: a
# symlink on macOS and Linux, a forwarder on Windows. Like assert_published, but
# for an arbitrary directory (the plugins-only directory as well as the bin
# directory). Windows plugin forwarders hardlink to the bin directory's shared
# rdd-forwarder copy, so the reference stays ${RDD_BIN_DIR}/rdd-forwarder.
assert_published_in() { # <dir> <target> <name>
    if is_windows; then
        local dest="$1/$3"
        assert_file_exist "${dest}"
        refute [ -L "${dest}" ]
        assert [ "${dest}" -ef "${RDD_BIN_DIR}/rdd-forwarder${EXE}" ]
        local shim="$1/${3%"${EXE}"}.shim"
        assert_file_exist "${shim}"
        assert_file_contains "${shim}" "$(basename "$2")"
    else
        assert_symlink_to "$2" "$1/$3"
    fi
}

# assert_not_published asserts <name> is gone from the bin directory, including
# its shim on Windows.
assert_not_published() { # <name>
    if is_windows; then
        assert_file_not_exist "${RDD_BIN_DIR}/$1"
        assert_file_not_exist "${RDD_BIN_DIR}/${1%"${EXE}"}.shim"
    else
        assert_link_not_exist "${RDD_BIN_DIR}/$1"
    fi
}

@test 'publishes bundled binaries when started from the app bundle' {
    load_var FAKE_BIN
    fake_rdd svc start
    assert_published "${FAKE_BIN}/rdd${EXE}" "rdd${EXE}"
    assert_published "${FAKE_BIN}/docker${EXE}" "docker${EXE}"
    assert_published "${FAKE_BIN}/helm${EXE}" "helm${EXE}"
    # kubectl and nerdctl are not bundled; they forward to rdd.
    assert_published "${FAKE_BIN}/rdd${EXE}" "kubectl${EXE}"
    assert_published "${FAKE_BIN}/rdd${EXE}" "nerdctl${EXE}"
    fake_rdd svc stop
}

@test 'publishes bundled docker CLI plugins into the bin and plugin directories' {
    load_var FAKE_BIN FAKE_PLUGIN_SRC
    fake_rdd svc start
    # Each bundled docker CLI plugin is reachable both as a standalone command in
    # the bin directory and in the plugins-only directory docker searches via
    # cliPluginsExtraDirs.
    for plugin in "docker-buildx${EXE}" "docker-compose${EXE}"; do
        assert_published_in "${RDD_BIN_DIR}" "${FAKE_PLUGIN_SRC}/${plugin}" "${plugin}"
        assert_published_in "${RDD_DOCKER_CLI_PLUGINS}" "${FAKE_PLUGIN_SRC}/${plugin}" "${plugin}"
    done
    fake_rdd svc stop
}

@test 'leaves working links untouched when started standalone' {
    load_var FAKE_BIN
    # The bundle run's links still resolve, so a standalone rdd leaves its own
    # rdd and kubectl links alone and never touches docker or helm.
    rdd svc start
    assert_published "${FAKE_BIN}/rdd${EXE}" "rdd${EXE}"
    assert_published "${FAKE_BIN}/docker${EXE}" "docker${EXE}"
    assert_published "${FAKE_BIN}/helm${EXE}" "helm${EXE}"
    assert_published "${FAKE_BIN}/rdd${EXE}" "kubectl${EXE}"
}

@test 'updates the links when the bundle changes and rdd runs from it again' {
    load_var FAKE_BIN
    # Add a tool and drop one, then restart from the bundle. The added tool
    # shares the nerdctl multicall name; the bundled binary must win.
    echo "stand-in nerdctl" >"${FAKE_BIN}/nerdctl${EXE}"
    rm "${FAKE_BIN}/helm${EXE}"
    fake_rdd svc start
    assert_published "${FAKE_BIN}/nerdctl${EXE}" "nerdctl${EXE}"
    # The dropped tool's entry (and its shim, on Windows) is gone.
    assert_not_published "helm${EXE}"
    # Unchanged entries are still published.
    assert_published "${FAKE_BIN}/rdd${EXE}" "rdd${EXE}"
    assert_published "${FAKE_BIN}/docker${EXE}" "docker${EXE}"
    assert_published "${FAKE_BIN}/rdd${EXE}" "kubectl${EXE}"
    fake_rdd svc stop
}

@test 'repairs missing or dangling rdd and kubectl links when started standalone' {
    load_var FAKE_BIN
    # Simulate an instance whose app was deleted: an rdd run from a throwaway
    # directory links rdd and kubectl to itself, then removing that directory
    # leaves the links dangling. Driving this through rdd avoids depending on
    # how the shell creates symlinks, which differs under MSYS2.
    rm -f "${RDD_BIN_DIR}/rdd${EXE}" "${RDD_BIN_DIR}/kubectl${EXE}"
    throwaway="${BATS_TEST_TMPDIR}/throwaway"
    mkdir -p "${throwaway}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${throwaway}/rdd${EXE}"
    "${throwaway}/rdd${EXE}" svc start 3>&- 4>&-
    "${throwaway}/rdd${EXE}" svc stop 3>&- 4>&-
    try --max 30 --delay 1 -- rm -rf "${throwaway}"
    # The standalone rdd repairs its own rdd and kubectl links to point at the
    # running binary. A standalone install does not publish forwarders, so these
    # are symlinks wherever the host allows them, as the CI runners do; without
    # that privilege Windows falls back to hardlinks and these checks fail.
    rdd svc start
    standalone="${PATH_REPO_ROOT}/bin/rdd${EXE}"
    assert_symlink_to "${standalone}" "${RDD_BIN_DIR}/rdd${EXE}"
    assert_symlink_to "${standalone}" "${RDD_BIN_DIR}/kubectl${EXE}"
    # The bundle's nerdctl entry still resolves, so the repair leaves it in place.
    assert_published "${FAKE_BIN}/nerdctl${EXE}" "nerdctl${EXE}"
    # The unrelated docker entry from the bundle run is left in place.
    assert_published "${FAKE_BIN}/docker${EXE}" "docker${EXE}"
}

@test 'prunes dangling tool links left after an uninstall' {
    # Publish from a throwaway bundle, then delete it so its links dangle — the
    # uninstall case. Driving setup through rdd gives real links the daemon
    # recognizes (MSYS2 ln -s would not).
    resources=resources
    is_macos && resources=Resources
    bundle="${BATS_TEST_TMPDIR}/Gone.app/Contents/${resources}/${OS}/bin"
    mkdir -p "${bundle}"
    cp "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${bundle}/rdd${EXE}"
    echo "stand-in docker" >"${bundle}/docker${EXE}"
    is_windows && echo "stand-in forwarder" >"${bundle}/rdd-forwarder${EXE}"
    "${bundle}/rdd${EXE}" svc start 3>&- 4>&-
    "${bundle}/rdd${EXE}" svc stop 3>&- 4>&-
    assert_published "${bundle}/docker${EXE}" "docker${EXE}"
    try --max 30 --delay 1 -- rm -rf "${bundle}"
    # A standalone rdd prunes the now-dangling docker entry so it cannot shadow a
    # tool on PATH, and repairs its own rdd and kubectl links to itself.
    rdd svc start
    assert_not_published "docker${EXE}"
    standalone="${PATH_REPO_ROOT}/bin/rdd${EXE}"
    assert_symlink_to "${standalone}" "${RDD_BIN_DIR}/rdd${EXE}"
    assert_symlink_to "${standalone}" "${RDD_BIN_DIR}/kubectl${EXE}"
    assert_symlink_to "${standalone}" "${RDD_BIN_DIR}/nerdctl${EXE}"
}

@test 'publishes hardlinks when symlinks are disabled' {
    skip_on_windows "Windows publishes forwarders, not symlinks or the hardlink fallback"
    load_var FAKE_BIN
    # RDD_NO_SYMLINKS forces the hardlink fallback that a symlink-less Unix host
    # would hit, so the same publish runs on a system that lacks symlinks.
    export RDD_NO_SYMLINKS=1
    fake_rdd svc start
    assert_hardlink_to "${FAKE_BIN}/rdd${EXE}" "${RDD_BIN_DIR}/rdd${EXE}"
    assert_hardlink_to "${FAKE_BIN}/docker${EXE}" "${RDD_BIN_DIR}/docker${EXE}"
    # kubectl is not bundled; it hardlinks to rdd.
    assert_hardlink_to "${FAKE_BIN}/rdd${EXE}" "${RDD_BIN_DIR}/kubectl${EXE}"
    fake_rdd svc stop
}

@test 'forwarders run the bundled tool under the invoked name' {
    skip_on_unix "only Windows publishes forwarders"
    load_var FAKE_BIN
    # Publish forwarders from the bundle, then run the published kubectl. It is a
    # hardlink to the real rdd-forwarder.exe beside a kubectl.shim naming rdd.exe,
    # so this exercises the forwarder passing "kubectl" through as argv[0] into
    # rdd's multicall — a re-synced forwarder that used the target's name instead
    # would make rdd run as the daemon, not kubectl. `version --client` needs no
    # control plane.
    fake_rdd svc start
    run -0 "${RDD_BIN_DIR}/kubectl${EXE}" version --client
    assert_output --partial "Client Version"
    fake_rdd svc stop
}

# The checkout tests below run last, after every test that depends on a
# FAKE_BIN publish surviving into the next test (e.g. "leaves working links
# untouched when started standalone" and "repairs missing or dangling rdd and
# kubectl links when started standalone" both expect the bin directory to
# still hold the previous test's FAKE_BIN-rooted links). linkBinaries
# unconditionally wipes and rebuilds the whole bin directory from whichever
# source published last, so running a checkout publish in between would
# replace those FAKE_BIN links with CHECKOUT_BIN ones and break those tests'
# assumptions. Placing the checkout tests last avoids that ordering hazard.

@test 'publishes checkout binaries when started in developer mode' {
    load_var CHECKOUT_BIN CHECKOUT_TOOLS
    # In developer mode, rdd mirrors the checkout's resources/<os>/bin the same
    # way the app bundle test mirrors a bundle's bin directory, even though rdd
    # itself lives elsewhere (beside the checkout's rdd/bin, not inside
    # resources/<os>/bin).
    checkout_rdd svc start
    assert_published "${CHECKOUT_BIN}/rdd${EXE}" "rdd${EXE}"
    assert_published "${CHECKOUT_TOOLS}/docker${EXE}" "docker${EXE}"
    assert_published "${CHECKOUT_TOOLS}/helm${EXE}" "helm${EXE}"
    # kubectl and nerdctl are not bundled; they forward to rdd.
    assert_published "${CHECKOUT_BIN}/rdd${EXE}" "kubectl${EXE}"
    assert_published "${CHECKOUT_BIN}/rdd${EXE}" "nerdctl${EXE}"
    checkout_rdd svc stop
}

@test 'publishes checkout docker CLI plugins into the bin and plugin directories in developer mode' {
    load_var CHECKOUT_BIN CHECKOUT_PLUGIN_SRC
    test_plugin="docker-plugin-${RANDOM}${EXE}"
    create_file "${CHECKOUT_PLUGIN_SRC}/${test_plugin}" <<<"stand-in test plugin"
    checkout_rdd svc start
    # Each checkout docker CLI plugin is reachable both as a standalone command
    # in the bin directory and in the plugins-only directory docker searches
    # via cliPluginsExtraDirs, the same as the bundle case.
    assert_published_in "${RDD_BIN_DIR}" "${CHECKOUT_PLUGIN_SRC}/${test_plugin}" "${test_plugin}"
    assert_published_in "${RDD_DOCKER_CLI_PLUGINS}" "${CHECKOUT_PLUGIN_SRC}/${test_plugin}" "${test_plugin}"
    checkout_rdd svc stop
}
