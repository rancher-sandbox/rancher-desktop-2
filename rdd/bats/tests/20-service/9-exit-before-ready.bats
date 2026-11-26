load '../../helpers/load'

# A CLI waiting for the control plane must fail as soon as the service exits,
# instead of waiting out its 90s timeout. In the first test, one CLI starts
# serve and another CLI waits for it. The test freezes serve with SIGSTOP, so
# that serve still counts as running but cannot become ready, and then kills
# it. In the second test, the CLI that started serve waits for it, and serve
# exits because its controller manager fails to install a CRD.

local_setup_file() {
    rdd svc delete --timeout=120s
    rdd svc create
}

@test 'waiting CLI fails fast when the control plane exits before it is ready' {
    # MSYS2's kill signals only Cygwin processes and cannot stop a native one.
    if is_windows; then
        skip "cannot SIGSTOP a native Windows process"
    fi

    run -0 rdd svc paths pid_file
    pid_file=${output}

    rdd svc start --wait=false
    try --max 50 --delay 0.2 test -s "${pid_file}"
    run -0 cat "${pid_file}"
    pid=${output}
    kill -STOP "${pid}"

    # Kill serve only once the waiting CLI has seen it running. A CLI that
    # doesn't find serve running starts a new one instead.
    cli_log=${BATS_TEST_TMPDIR}/start.log
    rdd --log-level=info svc start >"${cli_log}" 2>&1 3>&- &
    cli_pid=$!
    try --max 100 --delay 0.2 assert_cli_waiting "${cli_log}"

    SECONDS=0
    kill -KILL "${pid}"
    cli_status=0
    wait "${cli_pid}" || cli_status=$?
    assert_equal "${cli_status}" 1
    run -0 echo "${SECONDS}"
    assert_output_lt 30
    run -0 cat "${cli_log}"
    assert_output --partial 'control plane exited before it became ready'
}

assert_cli_waiting() { # <log file>
    run -0 cat "$1"
    assert_output --partial 'control plane to be ready'
}

@test 'starting CLI fails fast when the controller manager makes serve exit' {
    # Start from a fresh datastore, because an existing notary CRD would make
    # the apply below fail.
    rdd svc delete --timeout=120s
    rdd svc create
    # With --controllers empty, serve runs only the builtin controllers, which
    # don't install any CRDs, so serve leaves the CRD below as applied.
    rdd svc start --controllers=""

    # This notary CRD has a single version, v1alpha99, that the embedded CRD
    # lacks. A newer rdd leaves such a CRD behind when the user downgrades.
    # The API server records v1alpha99 in storedVersions and rejects any
    # update that drops it from the spec, so installing the embedded CRD fails.
    rdd ctl apply --filename - <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: notaries.rdd.rancherdesktop.io
spec:
  group: rdd.rancherdesktop.io
  names:
    kind: Notary
    listKind: NotaryList
    plural: notaries
    singular: notary
  scope: Namespaced
  versions:
  - name: v1alpha99
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        x-kubernetes-preserve-unknown-fields: true
EOF
    rdd ctl wait crd notaries.rdd.rancherdesktop.io --for condition=Established --timeout=10s
    rdd svc stop

    SECONDS=0
    run -1 rdd svc start
    assert_output --partial 'exited before it became ready'
    run -0 echo "${SECONDS}"
    assert_output_lt 30

    run -0 rdd svc paths stderr_log
    run -0 cat "${output}"
    assert_output --partial 'failed to update CRD'
}
