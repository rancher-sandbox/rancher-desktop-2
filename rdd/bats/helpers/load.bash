set -o errexit -o nounset -o pipefail

# Make sure run() will execute all functions with errexit enabled
export BATS_RUN_ERREXIT=1

# Prevent MSYS from converting POSIX paths in arguments to Windows paths.
# Without this, arguments like '/passthrough/demo/hello' get mangled.
export MSYS_NO_PATHCONV=1

bats_require_minimum_version 1.10.0

absolute_path() {
    (
        cd "$1"
        pwd
    )
}

PATH_BATS_HELPERS=$(absolute_path "$(dirname "${BASH_SOURCE[0]}")")
PATH_BATS_ROOT=$(absolute_path "${PATH_BATS_HELPERS}/..")
PATH_BATS_LOGS=${PATH_BATS_ROOT}/logs

# Use fatal() to abort loading helpers; don't run any tests
fatal() {
    local fd=2
    # fd 3 might not be open if we're not fully under bats yet; detect that.
    [[ -e /dev/fd/3 ]] && fd=3
    echo "   $1" >&"${fd}"

    # Print (ugly) stack trace if we are outside any @test function
    if [[ -z "${BATS_SUITE_TEST_NUMBER:-}" ]]; then
        echo >&"${fd}"
        local frame=0
        while caller "${frame}" >&"${fd}"; do
            ((frame++))
        done
    fi
    exit 1
}

source "${PATH_BATS_ROOT}/lib/bats-support/load.bash"
source "${PATH_BATS_ROOT}/lib/bats-assert/load.bash"
source "${PATH_BATS_ROOT}/lib/bats-file/load.bash"

source "${PATH_BATS_HELPERS}/os.bash"
source "${PATH_BATS_HELPERS}/vm_template.bash"
source "${PATH_BATS_HELPERS}/utils.bash"
source "${PATH_BATS_HELPERS}/controller.bash"
source "${PATH_BATS_HELPERS}/instance.bash"
source "${PATH_BATS_HELPERS}/logs.bash"
source "${PATH_BATS_HELPERS}/numeric.bash"

# defaults.bash uses is_windows() from os.bash and
# validate_enum() and is_true() from utils.bash.
source "${PATH_BATS_HELPERS}/defaults.bash"

source "${PATH_BATS_HELPERS}/paths.bash"

# commands.bash uses is_containerd() from defaults.bash,
# is_windows() etc from os.bash,
# and PATH_* variables from paths.bash
source "${PATH_BATS_HELPERS}/commands.bash"

# Add repo-root/bin directory to the PATH. This is where the Makefile puts all compiled programs.
export PATH="${PATH_BATS_ROOT}/../bin:${PATH}"

# If called from foo() this function will call local_foo() if it exist.
call_local_function() {
    local func
    func="local_$(calling_function)"
    if [[ "$(type -t "${func}" || true)" = "function" ]]; then
        "${func}"
    fi
}

setup_file() {
    # We require bash 4; bash 3.2 (as shipped by macOS) seems to have
    # compatibility issues.
    local bash_version
    bash_version=$(semver "${BASH_VERSION}")
    if semver_gt 4.0.0 "${bash_version}"; then
        fail "Bash 4.0.0 is required; you have ${BASH_VERSION}"
    fi

    call_local_function
}

teardown_file() {
    # Stop the control plane but don't delete it, to preserve logs for debugging.
    # The next test run's setup will clean up and create a fresh instance.
    rdd svc stop || true

    call_local_function
}

setup() {
    # Write test markers to RDD log files for easier debugging.
    # Skip if the log directory doesn't exist (test may not start a service).
    if [[ -d "${RDD_LOG_DIR}" ]]; then
        local log
        for log in "${RDD_LOG_DIR}"/rdd.stderr.log "${RDD_LOG_DIR}"/rdd.stdout.log; do
            printf "=== BATS: %s %s ===\n" \
                "$(date +"%Y-%m-%dT%H:%M:%S%z")" \
                "${BATS_TEST_DESCRIPTION}" \
                >>"${log}" 2>/dev/null || true
        done
    fi

    call_local_function
}

teardown() {
    call_local_function
}
