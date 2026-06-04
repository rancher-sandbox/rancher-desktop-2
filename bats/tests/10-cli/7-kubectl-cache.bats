# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# End-to-end test of the rdd kubectl version resolver's download / cache
# lifecycle. A fake apiserver advertises an out-of-skew version (v1.99.0),
# and a fake mirror serves a matching fake kubectl. The Go server under
# the sibling fake-kube/server directory plays both roles. The fake
# kubectl prints "fake-kubectl: <args>"; a passing test proves the
# resolver downloaded, sha-verified, and exec'd it.

load '../../helpers/load'

local_setup_file() {
    GOOS=$(go env GOOS)
    export GOOS
    GOARCH=$(go env GOARCH)
    export GOARCH
    # EXE is set in commands.bash (".exe" on Windows, empty elsewhere)
    # and re-exported below for the rdd subprocess. Don't shadow it
    # locally.
    export EXE

    # Stage the fake kubectl into the mirror tree at the path the
    # resolver will GET when it sees serverVersion=v1.99.0.
    export MIRROR_ROOT=${BATS_FILE_TMPDIR}/mirror
    export MIRROR_BIN_DIR=${MIRROR_ROOT}/release/v1.99.0/bin/${GOOS}/${GOARCH}
    mkdir -p "${MIRROR_BIN_DIR}"
    # Build inside the helper module so go.mod resolution picks up the
    # sibling fake-kube/go.mod, not the parent rancher-desktop-daemon one.
    # The -o paths feed go.exe directly on Windows, so they need
    # winpath conversion for the same reason the server's path args do.
    (
        cd "${BATS_TEST_DIRNAME}/fake-kube" || exit
        go build -ldflags='-s -w' \
            -o "$(winpath "${MIRROR_BIN_DIR}/kubectl${EXE}")" \
            ./kubectl
        go build -ldflags='-s -w' \
            -o "$(winpath "${BATS_FILE_TMPDIR}/fake-kube-server${EXE}")" \
            ./server
    )
    SERVER_BIN=${BATS_FILE_TMPDIR}/fake-kube-server${EXE}

    PORT_FILE=${BATS_FILE_TMPDIR}/port
    export LOG_FILE=${BATS_FILE_TMPDIR}/server.log
    export GIT_VERSION_FILE=${BATS_FILE_TMPDIR}/git-version
    export VERSION_STATUS_FILE=${BATS_FILE_TMPDIR}/version-status
    # On MSYS, the server is a native Windows .exe that cannot read
    # MSYS-namespace paths (/tmp/..., /c/...). Convert each path arg
    # with winpath. Production rdd never crosses this boundary;
    # MSYS_NO_PATHCONV=1 in load.bash leaves URL-like paths alone.
    "${SERVER_BIN}" \
        --root "$(winpath "${MIRROR_ROOT}")" \
        --major 1 --minor 99 --git-version v1.99.0 \
        --git-version-file "$(winpath "${GIT_VERSION_FILE}")" \
        --version-status-file "$(winpath "${VERSION_STATUS_FILE}")" \
        --port-file "$(winpath "${PORT_FILE}")" \
        --log-file "$(winpath "${LOG_FILE}")" &
    SERVER_PID=$!
    # setup_file and teardown_file run in separate subshells, so the env
    # var alone would vanish; save_var persists it via BATS_RUN_TMPDIR.
    save_var SERVER_PID
    # Wait for the port file. Server picks an ephemeral port, so we read it back.
    local i
    for i in {1..50}; do
        [[ -s ${PORT_FILE} ]] && break
        sleep 0.1
    done
    [[ -s ${PORT_FILE} ]] || fail "fake-kube-server did not write a port file"
    PORT=$(<"${PORT_FILE}")
    export PORT

    KUBECONFIG_PATH=${BATS_FILE_TMPDIR}/kubeconfig
    export KUBECONFIG_PATH
    cat >"${KUBECONFIG_PATH}" <<EOF
apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: http://127.0.0.1:${PORT}
    insecure-skip-tls-verify: true
contexts:
- name: fake
  context:
    cluster: fake
    user: ""
current-context: fake
EOF

    export CACHE_DIR=${BATS_FILE_TMPDIR}/cache
}

# rdd_env runs rdd with RDD_CACHE_DIR, RDD_KUBECTL_MIRROR, and
# KUBECONFIG set via env(1) instead of exported by bash. On Git Bash
# for Windows, bash strips MSYS-root prefixes from exported env values
# before exec'ing native children, landing the resolver's cache writes
# on a different drive than the shell reads back from. env(1) sets the
# vars in its own process, so the values reach rdd.exe verbatim.
rdd_env() {
    env \
        "RDD_CACHE_DIR=$(winpath "${CACHE_DIR}")" \
        "RDD_KUBECTL_MIRROR=http://127.0.0.1:${PORT}" \
        "KUBECONFIG=$(winpath "${KUBECONFIG_PATH}")" \
        rdd "$@"
}

local_teardown_file() {
    # Deviates from the "no teardown_file" rule. The rule's intent is to
    # preserve rdd state for post-mortem inspection; an HTTP server bound
    # to an ephemeral port is the opposite — leaving it running just leaks
    # ports between sessions.
    if load_var SERVER_PID; then
        process_kill "${SERVER_PID}" 2>/dev/null || true
    fi
}

local_setup() {
    rm -rf "${CACHE_DIR}"
    : >"${LOG_FILE}"
    # Reset both /version override files so each test starts with the
    # default v1.99.0 / 200 behavior.
    rm -f "${GIT_VERSION_FILE}" "${VERSION_STATUS_FILE}"
    # Republish the kubectl checksum every test so the sha-mismatch test
    # cannot leak its zeroed fixture into a later run.
    write_kubectl_sha512
}

write_kubectl_sha512() {
    if is_macos; then
        shasum -a 512 "${MIRROR_BIN_DIR}/kubectl${EXE}" | awk '{print $1}' >"${MIRROR_BIN_DIR}/kubectl${EXE}.sha512"
    else
        sha512sum "${MIRROR_BIN_DIR}/kubectl${EXE}" | awk '{print $1}' >"${MIRROR_BIN_DIR}/kubectl${EXE}.sha512"
    fi
}

@test 'rdd kubectl downloads a version-matched kubectl on cache miss' {
    cached=${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.0${EXE}
    assert_file_not_exists "${cached}"

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    assert_output --partial 'Downloading kubectl v1.99.0'
    assert_file_executable "${cached}"
    assert_file_contains "${LOG_FILE}" '^GET /version'
    assert_file_contains "${LOG_FILE}" "^GET /release/v1.99.0/bin/${GOOS}/${GOARCH}/kubectl${EXE}.sha512$"
    assert_file_contains "${LOG_FILE}" "^GET /release/v1.99.0/bin/${GOOS}/${GOARCH}/kubectl${EXE}$"
}

@test 'rdd kubectl skips the download when the cache already has a match' {
    cache_subdir=${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}
    mkdir -p "${cache_subdir}"
    cp "${MIRROR_BIN_DIR}/kubectl${EXE}" "${cache_subdir}/kubectl-v1.99.0${EXE}"
    chmod 0755 "${cache_subdir}/kubectl-v1.99.0${EXE}"

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    refute_output --partial 'Downloading kubectl'
    assert_file_contains "${LOG_FILE}" '^GET /version'
    refute_file_contains "${LOG_FILE}" '/release/'
}

@test 'rdd kubectl errors when the mirror has no kubectl for the server version' {
    # Point the apiserver at v1.99.1, for which the mirror tree is empty.
    # The resolver's first GET (.sha512) hits a 404 and surfaces the error.
    echo v1.99.1 >"${GIT_VERSION_FILE}"

    run rdd_env kubectl get pods

    assert_failure
    assert_output --partial 'resolving kubectl version'
    assert_output --partial '404'
    refute_output --partial 'fake-kubectl:'
    assert_file_not_exists "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.1${EXE}"
}

@test 'rdd kubectl errors on sha512 mismatch' {
    # Replace the published checksum with a wrong one. The download
    # succeeds, the sha verification fails, and no cache file lands.
    echo "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" \
        >"${MIRROR_BIN_DIR}/kubectl${EXE}.sha512"

    run rdd_env kubectl get pods

    assert_failure
    assert_output --partial 'resolving kubectl version'
    assert_output --partial 'checksum mismatch'
    refute_output --partial 'fake-kubectl:'
    assert_file_not_exists "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.0${EXE}"
}

@test 'rdd kubectl falls through to embedded when the apiserver returns 500 on /version' {
    echo 500 >"${VERSION_STATUS_FILE}"

    # `kubectl version` (no --client) makes the resolver probe; --client
    # would short-circuit isClientOnly before the probe and never reach
    # the 500. Exit code is unasserted because modern kubectl's behavior
    # on a 500 from /version varies across versions; the test verifies
    # probe-then-fall-through, not embedded kubectl's exit code.
    run rdd_env kubectl version

    # Embedded kubectl runs and prints something; the resolver neither
    # downloaded nor errored.
    assert_output
    refute_output --partial 'fake-kubectl:'
    refute_output --partial 'resolving kubectl version'
    refute_file_contains "${LOG_FILE}" '/release/'

    # Count /version requests to prove the resolver probed: the resolver
    # sends one, then the embedded kubectl's `version` subcommand sends
    # one of its own. A regression that short-circuits the resolver
    # would drop the count to 1 — assert_file_contains alone cannot
    # tell that case from this one.
    run -0 awk '/^GET \/version$/ {n++} END {print n+0}' "${LOG_FILE}"
    version_requests=${output}
    if [[ ${version_requests} -lt 2 ]]; then
        run -0 cat "${LOG_FILE}"
        fail "expected >= 2 GET /version (resolver + embedded kubectl), got ${version_requests}: ${output}"
    fi
}
