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

local_setup_file() {
    skip_on_windows "Kubernetes context tests require Lima"

    # Isolate ~/.kube/config so tests never touch the developer's real kubeconfig.
    # Set KUBECONFIG before starting the service so the controller process inherits
    # it; service.Start uses exec.Command without an explicit Env, so it inherits
    # the caller's environment.
    export KUBECONFIG="${BATS_FILE_TMPDIR}/kube/config"
    mkdir -p "$(dirname "${KUBECONFIG}")"

    rdd svc delete
    rdd set --wait=false running=true kubernetes.enabled=true kubernetes.version="${K3S_VERSION}"
    rdd ctl wait --for=condition=KubernetesReady app/"${APP_NAME}" --timeout=360s
}

kube_current_context() {
    [[ -f "${KUBECONFIG}" ]] || {
        echo ""
        return 0
    }

    kubectl config current-context 2>/dev/null || echo ""
}

kube_context_exists() { # <context-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    kubectl config get-contexts -o name 2>/dev/null | grep -qx "$1"
}

kube_cluster_exists() { # <cluster-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    kubectl config get-clusters 2>/dev/null | tail -n +2 | grep -qx "$1"
}

kube_user_exists() { # <user-name>
    [[ -f "${KUBECONFIG}" ]] || return 1
    kubectl config get-users 2>/dev/null | tail -n +2 | grep -qx "$1"
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
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run -0 kube_context_exists "${context_name}"
}

@test "kubernetes controller creates cluster entry in user kubeconfig" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run -0 kube_cluster_exists "${context_name}"
}

@test "kubernetes controller creates user entry in user kubeconfig" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run -0 kube_user_exists "${context_name}"
}

@test "context entry points to the instance k3s API server" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"

    # Read the server URL from the instance kubeconfig (written by k3s).
    run -0 rdd svc paths config
    local instance_kubeconfig="${output}"

    run -0 kubectl --kubeconfig="${instance_kubeconfig}" \
        config view -o jsonpath='{.clusters[0].cluster.server}'
    local expected_server="${output}"

    run -0 kubectl config view \
        -o jsonpath="{.clusters[?(@.name==\"${context_name}\")].cluster.server}"
    assert_output "${expected_server}"
}

@test "kubernetes controller sets current-context when no healthy context exists" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    # The current-context probe runs in a goroutine after KubernetesReady is
    # set; poll until it resolves.
    try --max 6 --delay 2 -- \
        bash -c "kubectl config current-context 2>/dev/null | grep -qx '${context_name}'"

    run -0 kube_current_context
    assert_output "${context_name}"
}

@test "KubernetesReady=NotApplicable when kubernetes is disabled" {
    rdd set running=true kubernetes.enabled=false
    run -0 rdd ctl get app "${APP_NAME}" \
        -o jsonpath='{.status.conditions[?(@.type=="KubernetesReady")].reason}'
    assert_output "NotApplicable"
}

@test "kubernetes controller removes context entries when kubernetes is disabled" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    # removeKubeContext runs synchronously in Reconcile; the context should be
    # gone by the time KubernetesReady=NotApplicable is stamped (which rdd set
    # above already waited for via Settled).
    run -1 kube_context_exists "${context_name}"
    run -1 kube_cluster_exists "${context_name}"
    run -1 kube_user_exists "${context_name}"
}

@test "current-context is cleared when kubernetes is disabled" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run -0 kube_current_context
    refute_output "${context_name}"
}

@test "re-enable kubernetes to set up cleanup test" {
    rdd set --wait=false running=true kubernetes.enabled=true kubernetes.version="${K3S_VERSION}"
    rdd ctl wait --for=condition=KubernetesReady app/"${APP_NAME}" --timeout=360s
}

@test "current-context is set again after re-enabling kubernetes" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    try --max 6 --delay 2 -- \
        bash -c "kubectl config current-context 2>/dev/null | grep -qx '${context_name}'"
}

@test "stopping VM clears current-context" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    rdd set running=false
    # removeKubeContext fires on the NotRunning reconcile; poll until done.
    try --max 10 --delay 1 -- \
        bash -c "! kube_context_exists '${context_name}'"
    run -0 kube_current_context
    refute_output "${context_name}"
}

@test "stopping VM removes context from user kubeconfig" {
    local context_name="rancher-desktop-${RDD_INSTANCE}"
    run -1 kube_context_exists "${context_name}"
    run -1 kube_cluster_exists "${context_name}"
    run -1 kube_user_exists "${context_name}"
}
