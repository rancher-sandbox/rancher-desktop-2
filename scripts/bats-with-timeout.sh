#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Run a bats target with a timeout. Always writes a support bundle
# (process tree, pgid, wchan, open fds) at the end so CI artifacts carry
# the evidence needed to diagnose hangs and leaked processes. On timeout,
# additionally kills the target with SIGTERM then SIGKILL.
#
# Non-destructive state capture is unfiltered so the bundle shows sibling
# parallel bats targets too; analysis matches them by cmdline substring.
# Destructive steps (SIGQUIT, SIGKILL of leaked processes) are scoped to
# our own RDD_INSTANCE so they do not disturb sibling targets.
#
# Usage: bats-with-timeout.sh <seconds> <label> <command> [args...]

set -o errexit -o nounset -o pipefail

timeout_seconds=$1
label=$2
shift 2

instance="${RDD_INSTANCE:-2}"

# `rdd svc paths log_dir` is a pure local computation (see
# cmd/rdd/service_paths.go): it resolves the path from the instance
# suffix without touching the running service, so it is safe to call
# even when the target under test is hung.
log_dir=$(rdd svc paths log_dir)
mkdir -p "${log_dir}"
bundle_file="${log_dir}/support-bundle.log"

is_interesting_process() {
    case "$1" in
        bash|sh|bats|rdd|rdd.exe|qemu*|lima*|hostagent|*-controller|*-controller.exe)
            return 0
            ;;
    esac
    return 1
}

# Read a process's pgid. `ps -o pgid=` works on both Linux and macOS.
pgid_of() {
    ps -o pgid= -p "$1" 2>/dev/null | tr -d ' ' || true
}

# Read a process's cmdline as a single line.
cmdline_of() {
    if [ -r "/proc/$1/cmdline" ]; then
        tr '\0' ' ' <"/proc/$1/cmdline" 2>/dev/null || true
    else
        ps -o command= -p "$1" 2>/dev/null || true
    fi
}

# Check whether a cmdline belongs to the current RDD_INSTANCE. The
# instance name appears in any path derived from it (~/.rd<instance>/,
# rancher-desktop-<instance>/, ...) and in sh wrapper argv as
# `RDD_INSTANCE=<instance>`.
matches_our_instance() {
    case "$1" in
        *"${instance}"*) return 0 ;;
    esac
    return 1
}

dump_linux_proc() {
    local pid_dir pid comm
    for pid_dir in /proc/[0-9]*; do
        [ -d "$pid_dir" ] || continue
        pid=${pid_dir##*/}
        comm=$(tr -d '\0' <"$pid_dir/comm" 2>/dev/null || echo "?")
        if ! is_interesting_process "$comm"; then
            continue
        fi
        echo
        echo "--- pid=${pid} comm=${comm} ---"
        echo "pgid: $(pgid_of "$pid")"
        echo "state: $(grep -m1 ^State "$pid_dir/status" 2>/dev/null || echo ?)"
        echo "wchan: $(cat "$pid_dir/wchan" 2>/dev/null || echo ?)"
        echo "cmdline: $(tr '\0' ' ' <"$pid_dir/cmdline" 2>/dev/null || echo ?)"
        echo "fds:"
        ls -l "$pid_dir/fd/" 2>/dev/null | sed 's/^/  /' || true
    done
}

dump_macos_ps() {
    # macOS has no /proc; use ps and lsof instead.
    # ucomm gives the basename (comm gives the full path on macOS).
    local pids=()
    local pid comm
    while read -r pid comm; do
        if is_interesting_process "$comm"; then
            pids+=("$pid")
        fi
    done < <(ps -axo pid=,ucomm= 2>/dev/null || true)

    for pid in "${pids[@]}"; do
        echo
        echo "--- pid=${pid} ---"
        ps -p "$pid" -o pid,pgid,stat,wchan,command 2>/dev/null | sed 's/^/  /' || true
        if command -v lsof >/dev/null 2>&1; then
            echo "fds (lsof):"
            lsof -p "$pid" 2>/dev/null | sed 's/^/  /' || true
        fi
        # `sample` captures user-space call stacks on macOS without attaching.
        if command -v sample >/dev/null 2>&1; then
            echo "sample (1s):"
            sample "$pid" 1 -mayDie 2>/dev/null | sed 's/^/  /' || true
        fi
    done
}

dump_sockets() {
    if command -v ss >/dev/null 2>&1; then
        echo "=== Open sockets (ss -tupn) ==="
        ss -tupn 2>&1 || true
    elif command -v lsof >/dev/null 2>&1; then
        echo "=== Open sockets (lsof -iP) ==="
        lsof -iP 2>&1 || true
    elif command -v netstat >/dev/null 2>&1; then
        echo "=== Open sockets (netstat -an) ==="
        netstat -an 2>&1 || true
    fi
}

# Non-destructive capture: reads only, signals nothing. Safe to call at
# any time. Unfiltered so sibling parallel bats targets show up too.
capture_state() {
    local context=$1
    {
        echo "=== Support bundle for ${label} (${context}) at $(date -Iseconds 2>/dev/null || date) ==="
        echo "uname: $(uname -a)"
        echo "RDD_INSTANCE: ${instance}"

        echo
        echo "=== ps ==="
        # ps auxf is Linux-only; fall back to plain aux on macOS/BSD.
        ps auxf 2>/dev/null || ps aux 2>&1 || true

        echo
        echo "=== Per-process state ==="
        if [ -d /proc ]; then
            dump_linux_proc
        else
            dump_macos_ps
        fi

        echo
        dump_sockets

        echo
        echo "=== End of support bundle (${context}) ==="
    } >>"${bundle_file}" 2>&1
}

# Enumerate leaked process PIDs belonging to the current RDD_INSTANCE.
# `go_only=1` restricts to Go binaries (for SIGQUIT goroutine dumps);
# otherwise includes qemu/limactl drivers too.
our_leaked_pids() {
    local go_only=$1
    local pid comm
    while read -r pid comm; do
        if [ "$go_only" = 1 ]; then
            case "$comm" in
                rdd|rdd.exe|*-controller|*-controller.exe|lima-guestagent|hostagent) ;;
                *) continue ;;
            esac
        else
            case "$comm" in
                rdd|rdd.exe|*-controller|*-controller.exe|lima-guestagent|hostagent|qemu*|limactl*) ;;
                *) continue ;;
            esac
        fi
        if matches_our_instance "$(cmdline_of "$pid")"; then
            echo "$pid"
        fi
    done < <(ps -axo pid=,ucomm= 2>/dev/null || ps -eo pid=,ucomm= 2>/dev/null || true)
}

# SIGQUIT Go processes belonging to our RDD_INSTANCE so their goroutine
# stacks land in the preserved stderr logs. No-op if nothing is leaked.
sigquit_our_go_leaks() {
    local pid
    local leaked
    leaked=$(our_leaked_pids 1)
    if [ -z "$leaked" ]; then
        return
    fi
    {
        echo
        echo "=== SIGQUIT -> leaked Go processes (RDD_INSTANCE=${instance}) ==="
        for pid in $leaked; do
            echo "SIGQUIT pid=${pid} pgid=$(pgid_of "$pid") cmdline=$(cmdline_of "$pid")"
            kill -QUIT "$pid" 2>/dev/null || true
        done
    } >>"${bundle_file}" 2>&1
    # Let Go runtimes flush goroutine dumps to stderr before subsequent
    # steps terminate them.
    sleep 1
}

# SIGKILL any process still running under our RDD_INSTANCE so the CI
# runner is clean for later steps. No-op if nothing is leaked.
sigkill_our_leaks() {
    local pid
    local leaked
    leaked=$(our_leaked_pids 0)
    if [ -z "$leaked" ]; then
        return
    fi
    {
        echo
        echo "=== SIGKILL -> leaked processes (RDD_INSTANCE=${instance}) ==="
        for pid in $leaked; do
            echo "SIGKILL pid=${pid} pgid=$(pgid_of "$pid") cmdline=$(cmdline_of "$pid")"
            kill -KILL "$pid" 2>/dev/null || true
        done
    } >>"${bundle_file}" 2>&1
}

# Start the command in the background.
"$@" &
cmd_pid=$!

# Poll for the deadline or command completion. Must run in the main shell
# so we can reliably wait for both the dump and the command to finish
# before exiting (a backgrounded watchdog would be killed mid-dump when
# the main shell exits).
deadline=$(($(date +%s) + timeout_seconds))
while kill -0 "${cmd_pid}" 2>/dev/null; do
    if [ "$(date +%s)" -ge "${deadline}" ]; then
        echo "bats-with-timeout: ${label} exceeded ${timeout_seconds}s, capturing support bundle" >&2
        capture_state "timeout"
        sigquit_our_go_leaks
        echo "bats-with-timeout: sending SIGTERM to ${cmd_pid}" >&2
        kill -TERM "${cmd_pid}" 2>/dev/null || true
        # Give it 30s to shut down gracefully, then SIGKILL.
        kill_deadline=$(($(date +%s) + 30))
        while kill -0 "${cmd_pid}" 2>/dev/null; do
            if [ "$(date +%s)" -ge "${kill_deadline}" ]; then
                echo "bats-with-timeout: sending SIGKILL to ${cmd_pid}" >&2
                kill -KILL "${cmd_pid}" 2>/dev/null || true
                break
            fi
            sleep 1
        done
        break
    fi
    sleep 1
done

exit_code=0
wait "${cmd_pid}" || exit_code=$?

# Always capture a post-run bundle so leaked grandchildren get recorded
# even when the bats target itself succeeded. sigkill_our_leaks cleans up
# anything still matching our RDD_INSTANCE so sibling targets aren't
# confused and the CI runner exits clean.
capture_state "post-run"
sigkill_our_leaks

exit "${exit_code}"
