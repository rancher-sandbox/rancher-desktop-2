load '../../helpers/load'

# Windows PATH marker (issue #916). Under addPath=front/back rdd adds the bin
# directory to the user Environment Path and records a marker file; under manual
# it removes only the entry it marked and deletes the marker, leaving an entry it
# doesn't own (no marker) alone. The marker is Windows-only — Unix startup files
# use a fenced block instead — so the whole file is skipped off Windows.
#
# These tests drive the real daemon, which edits HKCU\Environment\Path. Teardown
# runs `rdd svc delete`, whose Unwind force-removes this instance's entry from
# Path regardless of the marker, so the suite leaves no entry behind.

APP_NAME="app"

# read_user_path loads the user (HKCU) Path into USER_PATH, normalized to lower
# case with forward slashes so a comparison ignores case and slash direction, as
# Windows does. It returns non-zero when the read fails, so a broken read can
# never masquerade as "the entry is absent".
read_user_path() {
    local raw
    raw=$(powershell.exe -NoProfile -NonInteractive -Command \
        "[Environment]::GetEnvironmentVariable('Path','User')") || return 1
    USER_PATH=$(printf '%s' "${raw}" | tr -d '\r' | tr '[:upper:]' '[:lower:]' | tr '\134' '/')
}

# bindir_in succeeds when the normalized Path in $1 contains BIN_DIR.
bindir_in() { # <normalized-path>
    local want
    want=$(printf '%s' "${BIN_DIR}" | tr '[:upper:]' '[:lower:]' | tr '\134' '/')
    [[ ";$1;" == *";${want};"* ]]
}

# assert_path_has_bindir / assert_path_lacks_bindir read the Path first, so a
# failed read fails the assertion (and keeps `try` polling) instead of passing.
assert_path_has_bindir() {
    read_user_path || return 1
    bindir_in "${USER_PATH}"
}

assert_path_lacks_bindir() {
    read_user_path || return 1
    ! bindir_in "${USER_PATH}"
}

# add_bindir_to_path adds the bin directory to the user Path the way a user
# would: directly in the registry, so no marker is created. reg.exe writes /d as
# literal REG_EXPAND_SZ data, unlike the .NET setter, which expands %VAR% and
# downgrades the value to REG_SZ.
add_bindir_to_path() {
    run -0 cygpath -w "${RDD_BIN_DIR}"
    local bindir=${output} current new
    current=$(reg_user_path) || current=""
    if [[ -n "${current}" ]]; then
        new="${current};${bindir}"
    else
        new="${bindir}"
    fi
    run -0 reg.exe add 'HKCU\Environment' /v Path /t REG_EXPAND_SZ /d "${new}" /f
}

# reg_user_path prints the raw user Path from the registry (no %VAR% expansion),
# or returns non-zero when the value does not exist.
reg_user_path() {
    local out
    out=$(reg.exe query 'HKCU\Environment' /v Path 2>/dev/null) || return 1
    printf '%s' "${out}" | sed -nE 's/^[[:space:]]*Path[[:space:]]+REG_(EXPAND_)?SZ[[:space:]]+//p' | tr -d '\r'
}

set_add_path() { # <front|back|manual>
    rdd ctl patch app "${APP_NAME}" --type=merge \
        -p "{\"spec\":{\"application\":{\"addPath\":\"$1\"}}}"
}

# bump_generation flips an unrelated spec field so the App generation advances
# and the PathManagement controller reconciles again, without touching addPath.
bump_generation() {
    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.spec.application.updates.enabled}'
    local new=true
    [[ "${output}" == "true" ]] && new=false
    rdd ctl patch app "${APP_NAME}" --type=merge \
        -p "{\"spec\":{\"application\":{\"updates\":{\"enabled\":${new}}}}}"
}

# wait_path_applied blocks until the PathManagement controller has processed the
# current App generation, so an assertion that the entry survives can't race the
# reconcile that might remove it.
wait_path_applied() {
    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.metadata.generation}'
    try --max 30 --delay 1 -- path_observed_generation "${output}"
}

path_observed_generation() { # <generation>
    run rdd ctl get app "${APP_NAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="PathManagementReady")].observedGeneration}'
    [[ "${output}" == "$1" ]]
}

local_setup_file() {
    skip_on_unix "the PATH marker is Windows-only"
    rdd svc delete || :
    # `rdd set` starts the control plane and creates the App singleton; running
    # stays false because PATH management is host-side and needs no VM.
    rdd set running=false

    # BIN_DIR must be the native Windows form that the controller writes into
    # the registry; paths.bash gives RDD_BIN_DIR the MSYS (/c/...) form, which
    # can never match.
    run -0 host_path "${RDD_BIN_DIR}"
    BIN_DIR=${output}
    save_var BIN_DIR
}

local_teardown_file() {
    is_windows || return 0
    # Unwind force-removes this instance's entry regardless of the marker.
    rdd svc delete || :
}

@test 'front adds the bin directory and records the marker' {
    load_var BIN_DIR
    set_add_path front
    try --max 30 --delay 1 -- assert_path_has_bindir
    assert_file_exist "${RDD_PATH_MARKER}"
}

@test 'manual removes the marked entry and deletes the marker' {
    load_var BIN_DIR
    set_add_path manual
    try --max 30 --delay 1 -- assert_path_lacks_bindir
    assert_file_not_exist "${RDD_PATH_MARKER}"
}

@test 'manual leaves an unmarked entry alone' {
    load_var BIN_DIR
    # The previous test left addPath=manual with no marker and our entry gone.
    # Add the bin directory the way a user would — directly in the registry, so
    # no marker exists — then force a manual reconcile and confirm it survives.
    # Using front to seed the entry would rewrite the marker on every reconcile
    # pass and race the delete.
    add_bindir_to_path
    assert_path_has_bindir

    bump_generation
    wait_path_applied

    assert_file_not_exist "${RDD_PATH_MARKER}"
    assert_path_has_bindir
}
