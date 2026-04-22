#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Run a bats target with a timeout. Always writes a support bundle
# (process tree, pgid, wchan, open file descriptors) at the end so CI artifacts
# carry the evidence needed to diagnose hangs and leaked processes. On timeout,
# additionally kills the target with SIGTERM then SIGKILL.
#
# Non-destructive state capture is unfiltered so the bundle shows sibling
# parallel bats targets too; analysis matches them by cmdline substring.
# Destructive steps (SIGQUIT, SIGKILL of leaked processes) are scoped to
# our own RDD_INSTANCE so they do not disturb sibling targets.
#
# Usage: bats-with-timeout.sh <seconds> <command> [args...]

set -o errexit -o nounset -o pipefail

timeout_seconds=$1
shift

instance="${RDD_INSTANCE:-2}"

# Locate the rdd binary relative to this script rather than via PATH,
# since CI runners do not add <repo>/bin to PATH.
script_dir=$(cd "$(dirname "$0")" && pwd)
rdd_bin="${script_dir}/../bin/rdd"
if [ ! -x "$rdd_bin" ] && [ -x "${rdd_bin}.exe" ]; then
    rdd_bin="${rdd_bin}.exe"
fi
if [ ! -x "$rdd_bin" ]; then
    echo "bats-with-timeout: rdd binary not found at $rdd_bin; run 'make' first" >&2
    exit 2
fi

# `rdd svc paths log_dir` is a pure local computation (see
# cmd/rdd/service_paths.go): it resolves the path from the instance
# suffix without touching the running service, so it is safe to call
# even when the target under test is hung.
log_dir=$("$rdd_bin" svc paths log_dir)
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
# `RDD_INSTANCE=<instance>`. Each pattern requires a terminator after
# the instance name (whitespace, `/`, or end of string) so a short
# instance like "bats-cli" does not match a sibling target's
# "bats-cli-extra" processes and trigger cross-target SIGKILL cleanup.
# Appending a sentinel space lets the end-of-string case match the
# whitespace-terminated patterns.
matches_our_instance() {
    local cmdline="$1 "
    case "$cmdline" in
        *"RDD_INSTANCE=${instance} "*) return 0 ;;
        *"/.rd${instance}/"*) return 0 ;;
        *"rancher-desktop-${instance}/"* | *"rancher-desktop-${instance} "*) return 0 ;;
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
        echo "file descriptors:"
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
    done < <(ps -a -x -o pid=,ucomm= 2>/dev/null || true)

    for pid in "${pids[@]}"; do
        echo
        echo "--- pid=${pid} ---"
        ps -p "$pid" -o pid,pgid,stat,wchan,command 2>/dev/null | sed 's/^/  /' || true
        if command -v lsof >/dev/null 2>&1; then
            echo "file descriptors (lsof):"
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
    local -a cmd
    if command -v ss >/dev/null 2>&1; then
        cmd=(ss --tcp --udp --processes --numeric)
    elif command -v lsof >/dev/null 2>&1; then
        cmd=(lsof -i -P)
    elif command -v netstat >/dev/null 2>&1; then
        cmd=(netstat -a -n)
    else
        return
    fi
    echo "=== Open sockets (${cmd[*]}) ==="
    "${cmd[@]}" 2>&1 || true
}

# Capture evidence of memory pressure or external process kills.
#
# macOS jetsam and the Linux OOM killer terminate processes with SIGKILL,
# which Go cannot catch — so the daemon leaves no crash log behind. The
# only evidence lives in kernel / unified-log messages. Without this
# dump, a daemon that vanished because the runner ran out of memory is
# indistinguishable from one that hung.
dump_memory_pressure() {
    echo "=== Memory stats ==="
    case "$(uname)" in
        Darwin)
            vm_stat 2>&1 || true
            echo
            # no-spell-check-next-line
            sysctl vm.swapusage 2>&1 || true
            ;;
        Linux)
            free -h 2>&1 || true
            ;;
    esac

    echo
    echo "=== Top processes by memory (top 20) ==="
    case "$(uname)" in
        Darwin)
            top -l 1 -n 20 -o mem -stats pid,command,mem,state 2>&1 | tail -25 || true
            ;;
        Linux)
            ps -eo pid,pgid,pmem,rss,comm --sort=-rss 2>&1 | head -21 || true
            ;;
    esac

    echo
    echo "=== Memory pressure / OOM events ==="
    case "$(uname)" in
        Darwin)
            # Jetsam (macOS memory pressure killer) entries land in the
            # unified log with sender=kernel. --last 1h covers any single
            # bats target (30-min timeout + slack). --style compact keeps
            # output small.
            if command -v log >/dev/null 2>&1; then
                local predicate=(
                    '(sender == "kernel") AND'
                    '('
                    '(eventMessage CONTAINS[c] "jetsam") OR '
                    # no-spell-check-next-line
                    '(eventMessage CONTAINS[c] "memorystatus") OR '
                    '(eventMessage CONTAINS[c] "low swap") OR '
                    # no-spell-check-next-line
                    '(eventMessage CONTAINS[c] "lowmem")'
                    ')'
                )
                log show --style compact --last 1h \
                    --predicate "${predicate[*]}" \
                    2>&1 | tail -100 || true
            fi
            ;;
        Linux)
            # OOM killer activations appear in dmesg. The ring buffer is
            # capped, so tail is sufficient.
            if command -v dmesg >/dev/null 2>&1; then
                dmesg 2>/dev/null | grep -iE 'OOM|killed process|out of memory' | tail -50 || true
            fi
            ;;
    esac
}

# Snapshot the current state of the rdd API server and (if wired up) the
# forwarded Docker daemon. Wraps every probe in `timeout` so a hung or
# dead daemon cannot block the capture — the common case for the
# failures this bundle is meant to diagnose. --kill-after=1 gives the
# child one second to shut down after SIGTERM before SIGKILL.
dump_api_state() {
    if [ ! -x "$rdd_bin" ]; then
        return
    fi

    if "$rdd_bin" service status 2>/dev/null | grep --quiet 'control plane has been started: true'; then
        local resource_types
        echo "=== rdd ctl get (overview) ==="
        resource_types=$("$rdd_bin" ctl api-resources --output=json \
            | jq -rc '.resources | map(select((.group // "") | test("rancherdesktop.io")) | .name) | join(",")' \
            || echo "apps,limavms,containers,images,volumes,containernamespaces")
        timeout --kill-after=1 10 \
            "$rdd_bin" ctl get "$resource_types" --all-namespaces 2>&1 || true

        echo
        echo "=== rdd ctl get events (by time) ==="
        timeout --kill-after=1 10 \
            "$rdd_bin" ctl get events --all-namespaces \
            --sort-by=.lastTimestamp 2>&1 | tail -100 || true

        echo
        echo "=== rdd ctl get (full YAML) ==="
        timeout --kill-after=1 15 \
            "$rdd_bin" ctl get "$resource_types" --all-namespaces --output=yaml 2>&1 || true
    else
        echo "=== rdd control plane is not running ==="
    fi

    # Docker state: test suites that exercise the container engine
    # forward the guest Docker socket to a host path. Skip silently
    # when it is not wired up (most bats targets do not use Docker).
    local docker_sock="${HOME}/.rd2/docker.sock"
    if [ -S "$docker_sock" ] && command -v docker >/dev/null 2>&1; then
        echo
        echo "=== docker ps -a (DOCKER_HOST=unix://${docker_sock}) ==="
        DOCKER_HOST="unix://${docker_sock}" \
            timeout --kill-after=1 10 docker ps --all --no-trunc 2>&1 || true

        echo
        echo "=== docker inspect (all containers) ==="
        local ids
        ids=$(DOCKER_HOST="unix://${docker_sock}" \
            timeout --kill-after=1 10 docker ps --all --quiet 2>/dev/null || true)
        if [ -n "$ids" ]; then
            # shellcheck disable=SC2086  # intentional word splitting
            DOCKER_HOST="unix://${docker_sock}" \
                timeout --kill-after=1 10 docker inspect $ids 2>&1 || true
        fi
    fi
}

# Non-destructive capture: reads only, signals nothing.
capture_state() {
    local context=$1
    {
        echo "=== Support bundle for RDD_INSTANCE=${instance} (${context}) at $(date --iso-8601=seconds 2>/dev/null || date) ==="
        echo "uname: $(uname -a)"

        echo
        echo "=== ps ==="
        # `ps --forest` is Linux-only; fall back to without on macOS/BSD.
        ps aux --forest 2>/dev/null || ps aux 2>&1 || true

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
        dump_memory_pressure

        echo
        dump_api_state

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
    done < <(ps -a -x -o pid=,ucomm= 2>/dev/null || ps -e -o pid=,ucomm= 2>/dev/null || true)
}

# SIGQUIT Go processes belonging to our RDD_INSTANCE so their goroutine
# stacks land in the preserved stderr logs. No-op if nothing is leaked.
sig_quit_our_go_leaks() {
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
sig_kill_our_leaks() {
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

"$@" &
cmd_pid=$!

# Poll for the deadline or command completion. Must run in the main shell
# so we can reliably wait for both the dump and the command to finish
# before exiting (a background watchdog would be killed mid-dump when
# the main shell exits).
deadline=$(($(date +%s) + timeout_seconds))
while kill -0 "${cmd_pid}" 2>/dev/null; do
    if [ "$(date +%s)" -ge "${deadline}" ]; then
        echo "bats-with-timeout: RDD_INSTANCE=${instance} exceeded ${timeout_seconds}s, capturing support bundle" >&2
        capture_state "timeout"
        sig_quit_our_go_leaks
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
# even when the bats target itself succeeded. sig_kill_our_leaks cleans up
# anything still matching our RDD_INSTANCE so sibling targets aren't
# confused and the CI runner exits clean.
capture_state "post-run"
sig_kill_our_leaks

exit "${exit_code}"
