#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Collect BATS logs into a single directory: service logs, preserved instance
# logs (moved to log_dir during deletion), and Lima instance logs.
# Usage: scripts/collect-bats-logs.sh [output-dir]
#
# Iterates over all BATS instances, resolves their log and Lima home
# directories via "rdd svc paths", and copies log files into output-dir.
#
# Output layout:
#   output-dir/{instance}/              — service logs (rdd.stdout, rdd.stderr)
#   output-dir/{instance}/{vm}/         — preserved instance logs
#   output-dir/{instance}/lima-{vm}/    — hostagent logs (ha.stdout, ha.stderr)

set -o errexit -o nounset -o pipefail
shopt -s nullglob

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "${SCRIPT_DIR}/.." && pwd)

output_dir=${1:-rdd-logs}

# Use .exe extension on Windows (WSL or MSYS2).
rdd="${REPO_ROOT}/bin/rdd"
if command -v wslpath >/dev/null 2>&1 || command -v cygpath >/dev/null 2>&1; then
    rdd="${REPO_ROOT}/bin/rdd.exe"
fi

# Pass RDD_INSTANCE to Windows binaries via WSL interop.
export WSLENV="${WSLENV:+${WSLENV}:}RDD_INSTANCE"

# Resolve a path from rdd, converting Windows paths to POSIX paths if needed.
resolve_path() {
    local p
    p=$("$rdd" svc paths "$1" | tr -d '\r') || return 1
    if command -v wslpath >/dev/null 2>&1; then
        p=$(wslpath -ua "$p")
    elif command -v cygpath >/dev/null 2>&1; then
        p=$(cygpath -u "$p")
    fi
    echo "$p"
}

# Copy log files (not symlinks) from a directory into the output.
collect_logs() {
    local src=$1 dest=$2
    [ -d "$src" ] || return 0
    mkdir -p "$dest"
    find "$src" -maxdepth 1 -type f -name '*.log' -exec cp {} "$dest/" \;
}

for instance in $(make --no-print-directory -C "${REPO_ROOT}/bats" bats-instances); do
    export RDD_INSTANCE="$instance"
    dest="${output_dir}/${instance}"

    # Service logs (rdd.stdout, rdd.stderr)
    if log_dir=$(resolve_path log_dir); then
        collect_logs "$log_dir" "$dest"
    fi

    # Preserved instance logs (moved to log_dir during instance deletion)
    if [ -n "${log_dir:-}" ]; then
        for vm_dir in "$log_dir"/*/; do
            vm_name=$(basename "$vm_dir")
            collect_logs "$vm_dir" "$dest/${vm_name}"
        done
    fi

    # Lima instance logs (for instances that survived teardown)
    if lima_home=$(resolve_path lima_home); then
        for vm_dir in "$lima_home"/*/; do
            vm_name=$(basename "$vm_dir")
            collect_logs "$vm_dir" "$dest/lima-${vm_name}"
        done
    fi
done

echo "Logs collected in ${output_dir}/"
