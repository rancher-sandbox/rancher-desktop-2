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

# shellcheck source=rdd/scripts/metrics.bash
source "${PATH_BATS_ROOT}/../scripts/metrics.bash"
metrics_init "$(absolute_path "${PATH_BATS_ROOT}/..")"

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

# docker.bash depends on defaults.bash, os.bash, and utils.bash, sourced above.
source "${PATH_BATS_HELPERS}/docker.bash"

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

    # bats prints nothing between the plan line and the first "ok", so a file
    # that boots a VM in local_setup_file hides that cost from every per-test
    # number. Time it here instead. call_local_function reads FUNCNAME[2] to
    # find local_setup_file, so it must stay a direct call from this frame.
    local start_ms
    start_ms=$(metrics_now_ms)
    call_local_function
    metrics_phase local_setup_file "${start_ms}"
}

# Dump each VM's boot journal and unit timings while the control plane is
# still up. The window between "Installing k3s to /usr/local/bin/k3s" and
# "Started Lightweight Kubernetes" tells a slow runner apart from a change in
# our own code. Opt-in, because it costs seconds per file.
capture_guest_metrics() {
    [[ "${RDD_METRICS_GUEST:-}" == 1 ]] || return 0
    local vm
    while read -r vm; do
        [[ -n "${vm}" ]] || continue
        {
            echo "=== ${vm}: systemd-analyze blame ==="
            metrics_timeout 30 rdd limavm shell "${vm}" systemd-analyze blame
            echo "=== ${vm}: journalctl -b ==="
            metrics_timeout 60 rdd limavm shell "${vm}" journalctl -b --no-pager
            echo "=== ${vm}: /proc/diskstats ==="
            metrics_timeout 30 rdd limavm shell "${vm}" cat /proc/diskstats
        } >"${RDD_METRICS_DIR}/guest-$(basename "${BATS_TEST_FILENAME:-unknown}" .bats)-${vm}.txt" 2>&1 || true
    done < <(rdd ctl get limavms --all-namespaces \
        --output=jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null || true)
}

teardown_file() {
    # Stop the control plane between files so logs survive for debugging; the
    # next run's setup creates a fresh instance. RDD_KEEP_RUNNING=1 keeps the
    # engine up across files so a VM-booting suite boots once.
    local start_ms
    start_ms=$(metrics_now_ms)
    capture_guest_metrics
    if [[ "${RDD_KEEP_RUNNING:-}" != 1 ]]; then
        rdd svc stop || true
    fi

    call_local_function
    metrics_phase teardown_file "${start_ms}"
}

setup() {
    # Write test markers to RDD log files for easier debugging.
    # Skip if the log directory doesn't exist (test may not start a service).
    if [[ -d "${RDD_LOG_DIR}" ]]; then
        local log
        for log in "${RDD_STDERR_LOG}" "${RDD_STDOUT_LOG}"; do
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
