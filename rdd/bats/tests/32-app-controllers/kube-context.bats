# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Kubernetes context controller tests — verify that the kubernetes controller
# creates and cleans up the rancher-desktop-{instance} context in
# ~/.kube/config, sets current-context when appropriate, and reports the
# KubernetesReady condition on the App resource.

APP_NAME="app"
VM_NAME="rd"
K3S_VERSION="1.32.0"
CONTEXT_NAME="rancher-desktop-${RDD_INSTANCE}"

local_setup_file() {
    # Isolate ~/.kube/config so tests never touch the developer's real kubeconfig.
    # Set KUBECONFIG before starting the service so the controller process inherits
    # it; service.Start uses exec.Command without an explicit Env, so it inherits
    # the caller's environment.
    #
    # On Windows, rdd.exe is a native binary: it interprets /tmp/... as a path
    # from the drive root (C:\tmp\...) rather than MSYS2's /tmp which maps to a
    # different location. cygpath -m produces a mixed-format path (C:/msys64/...)
    # that both native Windows processes and MSYS2 agree on.
    if is_msys; then
        run -0 cygpath -m "${BATS_FILE_TMPDIR}/kube/config"
        KUBECONFIG="${output}"
    elif is_windows; then
        # Under WSL2 rdd is also a native binary; wslpath -m produces the
        # Windows-format path it expects.
        run -0 wslpath -m "${BATS_FILE_TMPDIR}/kube/config"
        KUBECONFIG="${output}"
    else
        KUBECONFIG="${BATS_FILE_TMPDIR}/kube/config"
    fi
    export KUBECONFIG
    mkdir -p "$(dirname "${KUBECONFIG}")"

    rdd svc delete
    rdd set --wait=false running=true kubernetes.enabled=true kubernetes.version="${K3S_VERSION}"
    # VM boot plus k3s startup can run several minutes on a slow CI runner, so
    # allow up to 900s to match app.bats. The outer bats-with-timeout still caps
    # the overall suite.
    rdd ctl wait --for=condition=KubernetesReady app/"${APP_NAME}" --timeout=900s
}

kube_current_context() {
    [[ -f "${KUBECONFIG}" ]] || {
        echo ""
        return 0
    }

    rdd kubectl config current-context 2>/dev/null || echo ""
}

kube_context_exists() { # <context-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    run -0 rdd kubectl config get-contexts -o name
    grep --quiet --line-regexp "$1" <<<"${output}"
}

kube_cluster_exists() { # <cluster-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    run -0 rdd kubectl config get-clusters
    grep --quiet --line-regexp "$1" <<<"${output}"
}

kube_user_exists() { # <user-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    run -0 rdd kubectl config get-users
    grep --quiet --line-regexp "$1" <<<"${output}"
}

kube_current_context_is() { # <expected-context>
    run rdd kubectl config current-context
    assert_output "$1"
}

@test "KubernetesReady condition is True when k3s is running" {
    run -0 rdd ctl get app "${APP_NAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="KubernetesReady")].status}'
    assert_output "True"
}

@test "KubernetesReady reason is Ready" {
    run -0 rdd ctl get app "${APP_NAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="KubernetesReady")].reason}'
    assert_output "Ready"
}

@test "kubernetes controller creates context entry in user kubeconfig" {
    kube_context_exists "${CONTEXT_NAME}"
}

@test "kubernetes controller creates cluster entry in user kubeconfig" {
    kube_cluster_exists "${CONTEXT_NAME}"
}

@test "kubernetes controller creates user entry in user kubeconfig" {
    kube_user_exists "${CONTEXT_NAME}"
}

@test "context entry points to the instance k3s API server" {
    # Read the server URL from the instance kubeconfig (written by k3s).
    run -0 rdd svc paths k3s_config
    instance_kubeconfig="${output}"

    run -0 rdd kubectl --kubeconfig="${instance_kubeconfig}" \
        config view -o jsonpath='{.clusters[0].cluster.server}'
    expected_server="${output}"

    run -0 rdd kubectl config view \
        -o jsonpath="{.clusters[?(@.name==\"${CONTEXT_NAME}\")].cluster.server}"
    assert_output "${expected_server}"
}

@test "rdd run targets the instance Kubernetes context" {
    # rdd run points KUBECONFIG at a throwaway file whose current context
    # is the instance, so a client talks to this cluster regardless of the
    # user's selected context. config current-context reads the file
    # without contacting the cluster. The user's kubeconfig is untouched.
    run_e -0 rdd run rdd kubectl config current-context
    assert_output "${CONTEXT_NAME}"
}

@test "rdd run prepends the instance bin directory to PATH" {
    # The instance bin directory leads PATH so tools bundled with the
    # instance shadow any global ones for the duration of the command.
    run -0 rdd svc paths short_dir
    bin_dir="${output}/bin"
    if is_msys; then
        # printenv is an MSYS program: its runtime rewrites the inherited
        # PATH into POSIX form, so convert rdd's native path to match.
        run -0 cygpath -u "${bin_dir}"
        bin_dir="${output}"
    elif is_windows; then
        # Under WSL2 rdd emits a Windows path; convert it to the POSIX form
        # printenv reports.
        run -0 wslpath -u "${bin_dir}"
        bin_dir="${output}"
    fi

    run_e -0 rdd run printenv PATH
    assert_equal "${output%%:*}" "${bin_dir}"
}

@test "kubernetes controller sets current-context when no healthy context exists" {
    # The current-context probe runs in a goroutine after KubernetesReady is
    # set; poll until it resolves.
    try --max 6 --delay 2 -- kube_current_context_is "${CONTEXT_NAME}"

    run -0 kube_current_context
    assert_output "${CONTEXT_NAME}"
}

@test "KubernetesReady=NotApplicable when kubernetes is disabled" {
    rdd set running=true kubernetes.enabled=false
    run -0 rdd ctl get app "${APP_NAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="KubernetesReady")].reason}'
    assert_output "NotApplicable"
}

@test "kubernetes controller removes context entries when kubernetes is disabled" {
    # removeKubeContext runs synchronously in Reconcile; the context should be
    # gone by the time KubernetesReady=NotApplicable is stamped (which rdd set
    # above already waited for via Settled).
    run -1 kube_context_exists "${CONTEXT_NAME}"
    run -1 kube_cluster_exists "${CONTEXT_NAME}"
    run -1 kube_user_exists "${CONTEXT_NAME}"
}

@test "current-context is cleared when kubernetes is disabled" {
    run -0 kube_current_context
    refute_output "${CONTEXT_NAME}"
}

@test "re-enable kubernetes to set up cleanup test" {
    rdd set --wait=false running=true kubernetes.enabled=true kubernetes.version="${K3S_VERSION}"
    # Allow 900s for VM boot plus k3s startup on a slow CI runner (see local_setup_file).
    rdd ctl wait --for=condition=KubernetesReady app/"${APP_NAME}" --timeout=900s
}

@test "current-context is set again after re-enabling kubernetes" {
    try --max 6 --delay 2 -- kube_current_context_is "${CONTEXT_NAME}"
}

@test "stopping VM clears current-context" {
    rdd set running=false
    # removeKubeContext fires on the NotRunning reconcile; poll until done.
    try --max 10 --delay 1 --until-fail -- kube_context_exists "${CONTEXT_NAME}"
    run -0 kube_current_context
    refute_output "${CONTEXT_NAME}"
}

@test "stopping VM removes context from user kubeconfig" {
    run -1 kube_context_exists "${CONTEXT_NAME}"
    run -1 kube_cluster_exists "${CONTEXT_NAME}"
    run -1 kube_user_exists "${CONTEXT_NAME}"
}
