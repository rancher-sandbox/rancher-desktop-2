#!/usr/bin/env bash

# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

# Shared helpers for the BATS timing instrumentation, sourced by
# bats-with-timeout.sh and by the bats helpers so both append to one set of
# TSV files.
#
# The default directory is inside the tree the BATS workflow uploads as the
# rdd-logs artifact, so CI collects the measurements with the service logs.

# metrics_init <repo-root>
metrics_init() {
    : "${RDD_METRICS_DIR:=${1}/rdd-logs/_diag/metrics}"
    export RDD_METRICS_DIR
    mkdir -p "${RDD_METRICS_DIR}"
}

# Milliseconds since the epoch.
metrics_now_ms() {
    # EPOCHREALTIME (bash 5) has microsecond resolution; BSD date does not
    # have %N, so older bash falls back to whole seconds.
    if [[ -n "${EPOCHREALTIME:-}" ]]; then
        echo $((${EPOCHREALTIME/[.,]/} / 1000))
    else
        echo $(($(date +%s) * 1000))
    fi
}

# metrics_header <name> <column>... Writes the header only for a new file.
metrics_header() {
    local name=$1
    shift
    [[ -n "${RDD_METRICS_DIR:-}" ]] || return 0
    local path="${RDD_METRICS_DIR}/${name}.tsv"
    [[ -e "${path}" ]] && return 0
    local IFS=$'\t'
    printf '%s\n' "$*" >>"${path}"
}

# metrics_record <name> <field>... Appends one TSV row.
metrics_record() {
    local name=$1
    shift
    [[ -n "${RDD_METRICS_DIR:-}" ]] || return 0
    local IFS=$'\t'
    printf '%s\n' "$*" >>"${RDD_METRICS_DIR}/${name}.tsv"
}

# metrics_phase <phase> <start-ms> One row for a phase of the current file.
# After perf_init, also appends a bats.<phase> record to the marks file.
metrics_phase() {
    local phase=$1 start_ms=$2 end_ms
    end_ms=$(metrics_now_ms)
    metrics_header phase file phase start_ms end_ms elapsed_ms
    metrics_record phase \
        "$(basename "${BATS_TEST_FILENAME:-unknown}")" "${phase}" \
        "${start_ms}" "${end_ms}" "$((end_ms - start_ms))"
    [[ -n "${RDD_PERF_MARKS:-}" ]] || return 0
    printf '%s metric=bats.%s value=%s unit=ms\n' \
        "${end_ms}" "${phase}" "$((end_ms - start_ms))" >>"${RDD_PERF_MARKS}"
}

# perf_init <suite> Truncates the suite's two perf files and exports their
# paths. rdd appends its phase timings to RDD_PERF_LOG. The bats helpers
# append marks and their own phase timings to RDD_PERF_MARKS. Each suite gets
# its own pair, so parallel targets don't mix their records.
perf_init() {
    export RDD_PERF_SUITE=$1
    export RDD_PERF_LOG="${RDD_METRICS_DIR}/perf-${RDD_PERF_SUITE}.log"
    export RDD_PERF_MARKS="${RDD_METRICS_DIR}/perf-${RDD_PERF_SUITE}-marks.log"
    : >"${RDD_PERF_LOG}"
    : >"${RDD_PERF_MARKS}"
    # rdd is a native program on Windows and can't open an MSYS2 path; only
    # bash writes the marks file, so its path stays as it is.
    if command -v cygpath >/dev/null 2>&1; then
        RDD_PERF_LOG=$(cygpath -m "${RDD_PERF_LOG}")
    fi
}

# perf_mark file | perf_mark test <n> Appends a mark to the marks file.
# perf_flush labels the records after a file mark with the current bats file,
# and the records after a test mark with test <n>. A test of "-" means no
# test. A file mark also sets the test back to "-", so the records from
# setup_file don't count toward the previous file's last test.
perf_mark() {
    local kind=$1
    local value=${2:-}
    [[ -n "${RDD_PERF_MARKS:-}" ]] || return 0
    if [[ "${kind}" == file ]]; then
        value=$(basename "${BATS_TEST_FILENAME:-unknown}")
    fi
    local now_ms
    now_ms=$(metrics_now_ms)
    printf '%s mark=%s %s\n' "${now_ms}" "${kind}" "${value}" >>"${RDD_PERF_MARKS}"
}

# perf_flush Prints each record as an "rdd-perf:" line, labelled with the bats
# file and test marked before it. Records before the first mark get "-" for
# both. Prints at most 3,000 records, then one perf.dropped line with the
# number left out. Skips a line that doesn't start with a timestamp and a
# metric or mark, such as the tail of a line torn by a concurrent write.
perf_flush() {
    [[ -n "${RDD_PERF_MARKS:-}" ]] || return 0
    local now_ms
    now_ms=$(metrics_now_ms)
    # A mark is written before the work that it labels, so it has to sort
    # before an rdd record from the same millisecond. Listing the marks file
    # first does that, because sort -s keeps lines with equal timestamps in
    # input order. Callers run with pipefail, so a failing sort still fails
    # the pipeline.
    # shellcheck disable=SC2312
    sort -s -n -k1,1 "${RDD_PERF_MARKS}" "${RDD_PERF_LOG}" |
        awk -v suite="${RDD_PERF_SUITE}" -v now="${now_ms}" -v limit=3000 '
            BEGIN { file = "-"; test_n = "-" }
            !/^[0-9]+ (metric|mark)=/ { next }
            $2 == "mark=file" { file = ($3 == "" ? "-" : $3); test_n = "-"; next }
            $2 == "mark=test" { test_n = ($3 == "" ? "-" : $3); next }
            $2 ~ /^mark=/ { next }
            printed >= limit { dropped++; next }
            {
                printf "rdd-perf: v=1 suite=%s file=%s test=%s ts=%s %s\n", suite, file, test_n, $1, substr($0, length($1) + 2)
                printed++
            }
            END {
                if (dropped) {
                    printf "rdd-perf: v=1 suite=%s file=- test=- ts=%s metric=perf.dropped value=%d unit=lines\n", suite, now, dropped
                }
            }'
}

# metrics_timeout <seconds> <command>... Runs the command with a time limit.
metrics_timeout() {
    local seconds=$1
    shift
    # GNU timeout is absent from a stock macOS, so run the command bare when
    # it is missing.
    if command -v timeout >/dev/null 2>&1; then
        timeout --kill-after=1 "${seconds}" "$@"
    else
        "$@"
    fi
}
