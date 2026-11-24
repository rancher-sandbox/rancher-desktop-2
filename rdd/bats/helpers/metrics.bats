# bats file_tags=opensuse

load '../helpers/load'

local_setup() {
    RDD_METRICS_DIR=${BATS_TEST_TMPDIR}
    perf_init suite-a
}

@test 'perf_flush merges records and marks by time' {
    cat >"${RDD_PERF_LOG}" <<'EOF'
100 metric=cp.total value=5 unit=ms
300 metric=limavm.boot.total value=7 unit=ms vm=rd
500 metric=svc.stop value=9 unit=ms
EOF
    cat >"${RDD_PERF_MARKS}" <<'EOF'
200 mark=file a.bats
250 metric=bats.local_setup_file value=50 unit=ms
260 mark=test 1
300 mark=test 2
400 mark=test -
EOF
    run -0 perf_flush
    assert_output - <<'EOF'
rdd-perf: v=1 suite=suite-a file=- test=- ts=100 metric=cp.total value=5 unit=ms
rdd-perf: v=1 suite=suite-a file=a.bats test=- ts=250 metric=bats.local_setup_file value=50 unit=ms
rdd-perf: v=1 suite=suite-a file=a.bats test=2 ts=300 metric=limavm.boot.total value=7 unit=ms vm=rd
rdd-perf: v=1 suite=suite-a file=a.bats test=- ts=500 metric=svc.stop value=9 unit=ms
EOF
}

@test 'perf_flush resets the test at a file mark' {
    cat >"${RDD_PERF_MARKS}" <<'EOF'
100 mark=file a.bats
110 mark=test 3
200 mark=file b.bats
210 metric=bats.local_setup_file value=10 unit=ms
EOF
    run -0 perf_flush
    assert_output 'rdd-perf: v=1 suite=suite-a file=b.bats test=- ts=210 metric=bats.local_setup_file value=10 unit=ms'
}

@test 'perf_flush keeps quoted labels and skips lines without a metric or mark' {
    cat >"${RDD_PERF_LOG}" <<'EOF'
100 metric=limavm.boot.req value=5 unit=ms req="Boot scripts to be finished" result=done
garbage
200 metric=cp.to
12 value=3
EOF
    run -0 perf_flush
    assert_output - <<'EOF'
rdd-perf: v=1 suite=suite-a file=- test=- ts=100 metric=limavm.boot.req value=5 unit=ms req="Boot scripts to be finished" result=done
rdd-perf: v=1 suite=suite-a file=- test=- ts=200 metric=cp.to
EOF
}

@test 'perf_flush prints at most 3000 records' {
    write_records() {
        local i
        for ((i = 1; i <= 3005; i++)); do
            echo "${i} metric=cp.kine value=1 unit=ms"
        done
    }
    write_records >"${RDD_PERF_LOG}"
    run -0 perf_flush
    assert_equal "${#lines[@]}" 3001
    assert_line --index 2999 'rdd-perf: v=1 suite=suite-a file=- test=- ts=3000 metric=cp.kine value=1 unit=ms'
    assert_line --index 3000 --regexp '^rdd-perf: v=1 suite=suite-a file=- test=- ts=[0-9]+ metric=perf\.dropped value=5 unit=lines$'
}

@test 'metrics_phase writes a bats record to the marks file' {
    metrics_phase teardown_file 1000
    run -0 cat "${RDD_PERF_MARKS}"
    assert_output --regexp '^[0-9]+ metric=bats\.teardown_file value=[0-9]+ unit=ms$'
}

@test 'perf_mark and perf_flush are no-ops without perf_init' {
    unset RDD_PERF_MARKS
    perf_mark test 1
    run -0 perf_flush
    refute_output
}
