# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# End-to-end test of the rdd kubectl resolver's download/cache cycle. A
# fake apiserver (v1.99.0, far newer than the embedded kubectl) and fake
# mirror (fake-kube/server) stand in for the real ones. The mirror's
# stable-1.99.txt marker names v1.99.5, so a passing download test
# proves the resolver picked the latest patch release of the server's
# minor, sha-verified it, and exec'd it.

load '../../helpers/load'

local_setup_file() {
    GOOS=$(go env GOOS)
    export GOOS
    GOARCH=$(go env GOARCH)
    export GOARCH
    # EXE (.exe on Windows, set in commands.bash) is re-exported below
    # for the rdd subprocess — don't shadow it locally.
    export EXE

    # Stage the fake kubectl into the mirror tree at the path the
    # resolver will GET after reading v1.99.5 from stable-1.99.txt.
    export MIRROR_ROOT=${BATS_FILE_TMPDIR}/mirror
    export MIRROR_BIN_DIR=${MIRROR_ROOT}/release/v1.99.5/bin/${GOOS}/${GOARCH}
    mkdir -p "${MIRROR_BIN_DIR}"
    # Build inside fake-kube/ so go.mod resolution picks the sibling
    # module; -o paths need winpath for the same reason the server's do.
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
    # On MSYS the server is a native .exe that can't read MSYS-namespace
    # paths, so path args go through winpath (production rdd never does this).
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

# rdd_env sets vars via env(1), not bash export: on Git Bash, exported
# values get their MSYS-root prefix stripped before exec'ing native
# children, landing cache writes on the wrong drive.
rdd_env() {
    env \
        "RDD_CACHE_DIR=$(winpath "${CACHE_DIR}")" \
        "RDD_KUBECTL_MIRROR=http://127.0.0.1:${PORT}" \
        "KUBECONFIG=$(winpath "${KUBECONFIG_PATH}")" \
        rdd "$@"
}

local_teardown_file() {
    # Deviates from "no teardown_file": an ephemeral-port HTTP server
    # shouldn't survive between runs (unlike rdd state, which the rule protects).
    if load_var SERVER_PID; then
        process_kill "${SERVER_PID}" 2>/dev/null || true
    fi
}

local_setup() {
    rm -rf "${CACHE_DIR}"
    : >"${LOG_FILE}"
    # Reset /version overrides so each test starts at v1.99.0 / 200.
    rm -f "${GIT_VERSION_FILE}" "${VERSION_STATUS_FILE}"
    # Republish the kubectl checksum and version marker every test so the
    # sha-mismatch and missing-version tests cannot leak their fixtures
    # into a later run.
    write_kubectl_sha512
    write_stable_marker
}

write_kubectl_sha512() {
    if is_macos; then
        shasum -a 512 "${MIRROR_BIN_DIR}/kubectl${EXE}" | awk '{print $1}' >"${MIRROR_BIN_DIR}/kubectl${EXE}.sha512"
    else
        sha512sum "${MIRROR_BIN_DIR}/kubectl${EXE}" | awk '{print $1}' >"${MIRROR_BIN_DIR}/kubectl${EXE}.sha512"
    fi
}

write_stable_marker() {
    echo 'v1.99.5' >"${MIRROR_ROOT}/release/stable-1.99.txt"
}

place_cached_kubectl() { # <version, e.g. v1.99.2>
    mkdir -p "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}"
    cp "${MIRROR_BIN_DIR}/kubectl${EXE}" "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-${1}${EXE}"
    chmod 0755 "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-${1}${EXE}"
}

@test 'rdd kubectl downloads the latest patch release of the server minor on cache miss' {
    cached=${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.5${EXE}
    assert_file_not_exists "${cached}"

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    # v1.99.5 comes from the stable-1.99.txt marker, not from the
    # server's own v1.99.0.
    assert_output --partial 'Downloading kubectl v1.99.5'
    assert_file_executable "${cached}"
    assert_file_contains "${LOG_FILE}" '^GET /version'
    assert_file_contains "${LOG_FILE}" '^GET /release/stable-1.99.txt$'
    assert_file_contains "${LOG_FILE}" "^GET /release/v1.99.5/bin/${GOOS}/${GOARCH}/kubectl${EXE}.sha512$"
    assert_file_contains "${LOG_FILE}" "^GET /release/v1.99.5/bin/${GOOS}/${GOARCH}/kubectl${EXE}$"
}

@test 'rdd kubectl accepts any cached patch of the server minor' {
    # Patch 2 matches neither the server's v1.99.0 nor the marker's
    # v1.99.5 — only the minor version counts, and a cache hit skips
    # the mirror entirely.
    place_cached_kubectl v1.99.2

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    refute_output --partial 'Downloading kubectl'
    assert_file_contains "${LOG_FILE}" '^GET /version'
    refute_file_contains "${LOG_FILE}" '/release/'
}

@test 'rdd kubectl accepts a cached kubectl one minor newer than the server' {
    place_cached_kubectl v1.100.1

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    refute_output --partial 'Downloading kubectl'
    refute_file_contains "${LOG_FILE}" '/release/'
}

@test 'rdd kubectl rejects a cached kubectl older than the server' {
    # v1.98.7 is within kubectl's official ±1 skew, but cannot drive
    # features new in 1.99, so the resolver must download v1.99.5.
    place_cached_kubectl v1.98.7

    run -0 rdd_env kubectl get pods

    assert_output --partial 'fake-kubectl: get pods'
    assert_output --partial 'Downloading kubectl v1.99.5'
    assert_file_executable "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.5${EXE}"
}

@test 'rdd kubectl falls back to embedded when the mirror has no release for the server minor' {
    # No stable-1.98.txt exists on the mirror; the resolver warns and
    # runs the embedded kubectl as the final fallback.
    echo v1.98.0 >"${GIT_VERSION_FILE}"

    run -0 rdd_env kubectl version

    # 'Client Version' proves the embedded kubectl actually ran.
    assert_output --partial 'using embedded kubectl'
    assert_output --partial 'Client Version'
    refute_output --partial 'fake-kubectl:'
    refute_output --partial 'Downloading kubectl'
    assert_file_contains "${LOG_FILE}" '^GET /release/stable-1.98.txt$'
}

@test 'rdd kubectl errors when the mirror lacks the release the marker names' {
    # The marker names v1.99.9, but the mirror tree only holds v1.99.5.
    # The resolver's first GET (.sha512) hits a 404 and surfaces the error.
    echo 'v1.99.9' >"${MIRROR_ROOT}/release/stable-1.99.txt"

    run rdd_env kubectl get pods

    assert_failure
    assert_output --partial 'resolving kubectl version'
    assert_output --partial '404'
    refute_output --partial 'fake-kubectl:'
    assert_file_not_exists "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.9${EXE}"
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
    assert_file_not_exists "${CACHE_DIR}/kubectl/${GOOS}-${GOARCH}/kubectl-v1.99.5${EXE}"
}

@test 'rdd kubectl falls through to embedded when the apiserver returns 500 on /version' {
    echo 500 >"${VERSION_STATUS_FILE}"

    # `kubectl version` (no --client) reaches the probe; exit code is
    # unasserted since it varies by kubectl version — this only checks
    # probe-then-fall-through.
    run rdd_env kubectl version

    # Embedded kubectl ran; the resolver neither downloaded nor errored.
    assert_output
    refute_output --partial 'fake-kubectl:'
    refute_output --partial 'resolving kubectl version'
    refute_file_contains "${LOG_FILE}" '/release/'

    # Count /version hits: resolver + embedded kubectl's own `version`
    # call should total >=2. A regression that short-circuits the
    # resolver would drop this to 1, which assert_file_contains can't catch.
    run -0 awk '/^GET \/version$/ {n++} END {print n+0}' "${LOG_FILE}"
    version_requests=${output}
    if [[ ${version_requests} -lt 2 ]]; then
        run -0 cat "${LOG_FILE}"
        fail "expected >= 2 GET /version (resolver + embedded kubectl), got ${version_requests}: ${output}"
    fi
}
