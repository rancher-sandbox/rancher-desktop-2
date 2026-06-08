to_lower() {
    echo "$@" | tr '[:upper:]' '[:lower:]'
}

to_upper() {
    echo "$@" | tr '[:lower:]' '[:upper:]'
}

is_true() {
    # case-insensitive check; false values: '', '0', 'no', and 'false'
    local value
    value=$(to_lower "$1")
    [[ ! ${value} =~ ^(0|no|false)?$ ]]
}

is_false() {
    ! is_true "$1"
}

bool() {
    if "$@"; then
        echo "true"
    else
        echo "false"
    fi
}

is_ci() {
    is_true "${CI:-}"
}

run_e() {
    run --separate-stderr "$@"
}

# Ensure that the variable contains a valid value, e.g.
# `validate_enum VAR value1 value2`
validate_enum() {
    local var=$1
    shift
    for value in "$@"; do
        if [[ ${!var} == "${value}" ]]; then
            return
        fi
    done
    fatal "${var}=${!var} is not a valid setting; select from [$*]"
}

# Ensure that the variable contains a valid semver (major.minor.path) version, e.g.
# `validate_semver RD_K3S_MAX`
validate_semver() {
    local var=$1
    if ! semver_is_valid "${!var}"; then
        fatal "${var}=${!var} is not a valid semver value (major.minor.patch)"
    fi
}

assert_nothing() {
    # This is a no-op, used to show that run() has been used to continue the
    # test even when the command failed, but the failure itself is ignored.
    true
}

########################################################################

assert=assert
refute=refute

before() {
    local assert=refute
    local refute=assert
    "$@"
}

refute_success() {
    assert_failure
}

refute_failure() {
    assert_success
}

refute_not_exists() {
    assert_exists "$@"
}

refute_file_exists() {
    assert_file_not_exists "$@"
}

refute_file_contains() {
    assert_file_not_contains "$@"
}

########################################################################

extract_msg() {
    sed -n '/.*msg="\(.*\)"$/{ s//\1/; s/\\"/"/g; p; }' <<<"${output}"
}

# Convert raw string into properly quoted JSON string
json_string() {
    echo -n "$1" | jq --raw-input --raw-output @json
}

# Join list elements by separator after converting them via the mapping function
# Examples:
#   join_map "/" echo usr local bin            =>   usr/local/bin
#   join_map ", " json_string a b\ c\"d\\e f   =>   "a", "b c\"d\\e", "f"
join_map() {
    local sep=$1
    local map=$2
    shift 2

    local elem
    local result=""
    for elem in "$@"; do
        elem=$(eval "${map}" '"$elem"')
        if [[ -z ${result} ]]; then
            result=${elem}
        else
            result="${result}${sep}${elem}"
        fi
    done
    echo "${result}"
}

# Extract raw value from a JSON object in a string (last argument).
# Note that when capturing $output, you may need to use `run --separate-stderr`
# to avoid also capturing stderr and ending up with invalid JSON.
jq_raw() {
    local args=("$@")
    local last=$((${#args[@]} - 1))
    local json=${args[${last}]}
    unset "args[${last}]"

    run jq --raw-output "${args[@]}" <<<"${json}"
    if [[ -n ${output} ]]; then
        echo "${output}"
        if [[ ${output} == null ]]; then
            status=1
        fi
    elif ((status == 0)); then
        # The command succeeded, so we should be able to run it again without error
        # If the jq command emitted a newline, then we want to emit a newline too.
        # shellcheck disable=SC2312 # wc can't fail
        if [[ "$(jq --raw-output "${args[@]}" <<<"${json}" | wc -c)" -gt 0 ]]; then
            echo ""
        fi
    fi
    return "${status}"
}

# Run jq_raw on the current $output
jq_output() {
    jq_raw "$@" "${output}"
}

# semver returns the first semver version from its first argument (which may be multiple lines).
# It does not include pre-release markers or build ids.
# It will match major.minor, or even just major if it can't find major.minor.patch.
# The returned version will always be a major.minor.patch string.
# Each part will have leading zeros removed.
# semver will fail when the input contains no number.
semver() {
    local input=$1
    local semver
    semver=$(awk 'match($0, /([0-9]+\.[0-9]+\.[0-9]+)/) {print substr($0, RSTART, RLENGTH); exit}' <<<"${input}")
    if [[ -z ${semver} ]]; then
        semver=$(awk 'match($0, /([0-9]+\.[0-9]+)/) {print substr($0, RSTART, RLENGTH); exit}' <<<"${input}")
    fi
    if [[ -z ${semver} ]]; then
        semver=$(awk 'match($0, /([0-9]+)/) {print substr($0, RSTART, RLENGTH); exit}' <<<"${input}")
    fi
    if [[ -z ${semver} ]]; then
        return 1
    fi
    until [[ ${semver} =~ \..+\. ]]; do
        semver="${semver}.0"
    done
    sed -E 's/^0*([0-9])/\1/; s/\.0*([0-9])/.\1/g' <<<"${semver}"
}

# Check if the argument is a valid 3-tuple version number with no leading 0s and no newlines
semver_is_valid() {
    [[ ! $1 =~ $'\n' ]] && grep -q -E '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$' <<<"$1"
}

# All semver comparison functions will return false when called without any argument
# and return true when called with just a single argument.

# semver_eq checks that all specified arguments are equal to each other.
# (semver_eq and semver_neq don't really depend on the arguments being versions).
# `A = B = C`
semver_eq() {
    # shellcheck disable=SC2312 # sort and wc can't fail.
    [[ $# -gt 0 ]] && [[ $(printf "%s\n" "$@" | sort --unique | wc -l) -eq 1 ]]
}

# semver_neq checks that all arguments are unique. `semver_neq A B C` is not the same as
# `A ≠ B ≠ C` because semver_neq will also return a failure if `A = C`.
# `(A ≠ B) & (A ≠ C) & (B ≠ C)`
semver_neq() {
    # shellcheck disable=SC2312 # sort can't fail.
    [[ $# -gt 0 ]] && printf "%s\n" "$@" | sort | sort --check=silent --unique
}

# `A ≤ B ≤ C`
semver_lte() {
    [[ $# -gt 0 ]] && printf "%s\n" "$@" | sort --check=silent --version-sort
}

# `A < B < C`
semver_lt() {
    [[ $# -gt 0 ]] && semver_lte "$@" && semver_neq "$@"
}

# `A ≥ B ≥ C`
semver_gte() {
    [[ $# -gt 0 ]] && printf "%s\n" "$@" | sort --check=silent --reverse --version-sort
}

# `A > B > C`
semver_gt() {
    [[ $# -gt 0 ]] && semver_gte "$@" && semver_neq "$@"
}

########################################################################

get_setting() {
    run rdctl api /settings
    assert_success
    jq_output "$@"
}

this_function() {
    echo "${FUNCNAME[1]}"
}

calling_function() {
    echo "${FUNCNAME[2]}"
}

# Write a comment to the TAP stream.
# Set CALLER to print a calling function higher up in the call stack.
comment() {
    local prefix=""
    if is_true "${RDD_TRACE}"; then
        local caller="${CALLER:-$(calling_function)}"
        prefix="($(date -u +"%FT%TZ"): ${caller}): "
    fi
    local line
    while IFS= read -r line; do
        if [[ -e /dev/fd/3 ]]; then
            printf "# %s%s\n" "${prefix}" "${line}" >&3
        else
            printf "# %s%s\n" "${prefix}" "${line}" >&2
        fi
    done <<<"$*"
}

# Write a comment to the TAP stream if RDD_TRACE is set.
# Set CALLER to print a calling function higher up in the call stack.
trace() {
    if is_true "${RDD_TRACE}"; then
        local calling_function
        calling_function=$(calling_function)
        CALLER=${CALLER:-${calling_function}} comment "$@"
    fi
}

# try runs the specified command until it either succeeds, or --max attempts
# have been made (with a --delay seconds sleep in between).
# With --until-fail, waits until the command fails instead of succeeds.
# With --per-try-timeout DUR, each attempt is wrapped in `timeout(1)` so a
# single hung call cannot stall the whole retry budget; DUR accepts any
# timeout(1) suffix (s, m, h). The attempt exits non-zero (124 on SIGTERM,
# 137 on SIGKILL) and counts like any other failure toward --max.
#
# Right now the command is **always** run with --separate-stderr, and stderr
# is output after all of stdout. This is subject to change, if we can figure
# out a way to detect if the caller used `run --separate-stderr try …` or not.
try() {
    local max=24
    local delay=5
    local per_try_timeout=""
    local until_fail=0

    while [[ $# -gt 0 ]] && [[ $1 == -* ]]; do
        case "$1" in
        --max)
            max=$2
            shift
            ;;
        --delay)
            delay=$2
            shift
            ;;
        --per-try-timeout)
            per_try_timeout=$2
            shift
            ;;
        --until-fail)
            until_fail=1
            ;;
        --)
            shift
            break
            ;;
        *)
            printf "Usage error: unknown flag '%s'" "$1" >&2
            return 1
            ;;
        esac
        shift
    done

    local count=0
    while true; do
        if [[ -n "${per_try_timeout}" ]]; then
            # --verbose logs "sending signal TERM" to stderr so the try
            # log distinguishes a timeout kill from a plain non-zero
            # exit. --kill-after=5s escalates SIGTERM to SIGKILL when
            # the command ignores the graceful signal, so a hung retry
            # cannot stall the loop.
            run_e timeout --verbose --kill-after=5s "${per_try_timeout}" "$@"
        else
            run_e "$@"
        fi
        local success
        if ((until_fail)); then
            success=$((status != 0))
        else
            success=$((status == 0))
        fi
        if ((success || ++count >= max)); then
            trace "${count}/${max} tries: $*"
            break
        fi
        sleep "${delay}"
    done
    echo "${output}"
    if [[ -n "${stderr:-}" ]]; then
        echo "${stderr}" >&2
    fi
    # When using --until-fail, return 0 if we successfully waited for failure, 1 if we timed out
    if ((until_fail)); then
        return $((success ? 0 : 1))
    fi
    return "${status}"
}

image_without_tag_as_json_string() {
    local image=$1
    # If the tag looks like a port number and follows something that looks
    # like a domain name, then don't strip the tag (e.g. foo.io:5000).
    if [[ ${image##*:} =~ ^[0-9]+(/|$) && ${image%:*} =~ \.[a-z]+$ ]]; then
        json_string "${image}"
    else
        json_string "${image%:*}"
    fi
}

update_allowed_patterns() {
    local enabled=$1
    shift

    local patterns
    patterns=$(join_map ", " image_without_tag_as_json_string "$@")

    # If the enabled state changes, then the container engine will be restarted.
    # Record PID of the current daemon process so we can wait for it to be ready again.
    local pid
    local patterns_enabled
    patterns_enabled=$(get_setting .containerEngine.allowedImages.enabled)
    if [[ "${enabled}" != "${patterns_enabled}" ]]; then
        pid=$(get_service_pid "${CONTAINER_ENGINE_SERVICE}")
    fi

    rdctl api settings -X PUT --input - <<EOF
{
  "version": 8,
  "containerEngine": {
    "allowedImages": {
      "enabled": ${enabled},
      "patterns": [${patterns}]
    }
  }
}
EOF
    # Wait for container engine (and Kubernetes) to be ready again
    if [[ -n ${pid:-} ]]; then
        try --max 15 --delay 5 refute_service_pid "${CONTAINER_ENGINE_SERVICE}" "${pid}"
        wait_for_container_engine
        enabled=$(get_setting .kubernetes.enabled)
        if [[ "${enabled}" == "true" ]]; then
            wait_for_kubelet
        fi
    fi
}

# create_file path/to/file <<< "contents"
# Create a new file with the provided path; the contents of standard input will
# be written to that file.  Analogous to `cat >$1`.  Will create any parent
# directories.
create_file() {
    local dest=$1
    # On WSL, avoid creating files from within the Linux side; this leads to
    # issues where the WSL view of the filesystem is desynchronized from the
    # Windows view, so we end up having ghost files that can't be deleted from
    # Windows. MSYS2 writes directly to the Windows filesystem, so it's safe.
    if ! is_windows || is_msys; then
        mkdir -p "$(dirname "${dest}")"
        cat >"${dest}"
        return
    fi

    local contents # Base64 encoded file contents
    contents="$(base64)"

    local winParent
    local winDest
    winParent="$(wslpath -w "$(dirname "${dest}")")"
    winDest="$(wslpath -w "${dest}")"
    PowerShell.exe -NoProfile -NoLogo -NonInteractive -Command "New-Item -ItemType Directory -ErrorAction SilentlyContinue '${winParent}'" || true
    if [[ -z "${contents}" ]]; then
        PowerShell.exe -NoProfile -NoLogo -NonInteractive -Command "New-Item -ItemType File -Force '${winDest}'"
    else
        local command="[IO.File]::WriteAllBytes('${winDest}', \$([System.Convert]::FromBase64String('${contents}')))"
        PowerShell.exe -NoProfile -NoLogo -NonInteractive -Command "${command}"
    fi
}

# unique_filename /tmp/image .png
# will return /tmp/image.png, or /tmp/image_2.png, etc.
unique_filename() {
    local basename=$1
    local extension=${2:-}
    local index=1
    local suffix=""

    while true; do
        local filename="${basename}${suffix}${extension}"
        if [[ ! -e ${filename} ]]; then
            echo "${filename}"
            return
        fi
        suffix="_$((++index))"
    done
}

skip_unless_host_ip() {
    if using_windows_exe; then
        # Make sure the exit code is 0 even when netsh.exe or grep fails, in case errexit is in effect
        HOST_IP=$(netsh.exe interface ip show addresses 'vEthernet (WSL)' | grep -Po 'IP Address:\s+\K[\d.]+' || :)
        # The veth interface name changed at some time on Windows 11, so try the new name if the old one doesn't exist
        if [[ -z ${HOST_IP} ]]; then
            HOST_IP=$(netsh.exe interface ip show addresses 'vEthernet (WSL (Hyper-V firewall))' | grep -Po 'IP Address:\s+\K[\d.]+' || :)
        fi
    else
        # TODO determine if the Lima VM has its own IP address
        HOST_IP=""
    fi
    if [[ -z ${HOST_IP} ]]; then
        skip "Test requires a routable host ip address"
    fi
}

########################################################################

# Register one or more test commands for each k3s version in RD_K3S_VERSIONS.
# Versions can be filtered by RD_K3S_MIN and RD_K3S_MAX.
foreach_k3s_version() {
    local k3s_version
    for k3s_version in ${RD_K3S_VERSIONS}; do
        if semver_lte "${RD_K3S_MIN}" "${k3s_version}" "${RD_K3S_MAX}"; then
            local cmd
            for cmd in "$@"; do
                bats_test_function --description "${cmd} ${k3s_version}" -- _foreach_k3s_version "${k3s_version}" "${cmd}"
            done
        fi
    done
}

_foreach_k3s_version() {
    local RD_KUBERNETES_VERSION=$1
    local skip_kubernetes_version
    skip_kubernetes_version=$(cat "${BATS_FILE_TMPDIR}/skip-kubernetes-version" 2>/dev/null || echo none)
    if [[ ${skip_kubernetes_version} == "${RD_KUBERNETES_VERSION}" ]]; then
        skip "All remaining tests for Kubernetes ${RD_KUBERNETES_VERSION} are skipped"
    fi
    "$2"
}

# Tests can call mark_k3s_version_skipped to skip the rest of the tests within
# this iteration of foreach_k3s_version.
mark_k3s_version_skipped() {
    echo "${RD_KUBERNETES_VERSION}" >"${BATS_FILE_TMPDIR}/skip-kubernetes-version"
}

########################################################################

_var_filename() {
    # Can't use BATS_SUITE_TMPDIR because it is unset outside of @test functions
    echo "${BATS_RUN_TMPDIR}/var_$1"
}

# Save env variables on disk, so they can be reloaded in different tests.
# This is mostly useful if calculating the setting takes a long time.
# Returns false if any variable was unbound, but will continue saving remaining variables.
# `save_var VAR1 VAR2`
save_var() {
    local res=0
    local var
    for var in "$@"; do
        # Using [[ -v $var ]] requires bash 4.2 but macOS only ships with 3.2
        if [[ -n "${!var+exists}" ]]; then
            printf "%s=%q\n" "${var}" "${!var}" >"$(_var_filename "${var}")"
        else
            res=1
        fi
    done
    return "${res}"
}

# Load env variables saved by `save_var`. Returns an error if any of the variables
# had not been saved, but will continue to try to load the remaining variables.
# `load_var VAR1 VAR2`
load_var() {
    local res=0
    local var
    for var in "$@"; do
        local file
        file=$(_var_filename "${var}")
        if [[ -r ${file} ]]; then
            # shellcheck disable=SC1090 # Can't follow non-constant source
            source "${file}"
        else
            res=1
        fi
    done
    return "${res}"
}
