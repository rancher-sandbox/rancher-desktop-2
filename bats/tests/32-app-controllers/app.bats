# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# App controller lifecycle bats tests controller's singleton creation, LimaVM ownership,
# running propagation, condition mirroring, and deletion.

APP_NAME="app"
VM_NAME="rd"
INPUT_CM_NAME="rd"
APP_VALIDATOR_CONFIG="app-validator"
K3S_VERSION="1.32.0"

delete_app() {
    # Stop the VM first so the LimaVM finalizer clears quickly.
    rdd set running=false 2>/dev/null || true
    rdd ctl delete app "${APP_NAME}" --ignore-not-found
    # Wait for full deletion so that create_app always starts with no
    # pre-existing App resource. Without this, rdd ctl apply in create_app
    # can update a still-terminating App, which the controller treats as a
    # deletion request — no LimaVM is ever created.
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=120s 2>/dev/null || true
}

create_app() {
    local running=${1:-false}
    delete_app

    rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: ${APP_NAME}
spec:
  namespace: ${RDD_NAMESPACE}
  running: ${running}
EOF
}

local_setup_file() {
    setup_rdd_control_plane
}

@test "create App resource" {
    create_app false
}

@test "verify App is cluster-scoped and accessible without a namespace" {
    run -0 rdd ctl get app "${APP_NAME}"
}

@test "verify App has cleanup finalizer" {
    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test "wait for LimaVM to be created" {
    rdd ctl wait --for=create limavm/"${VM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "verify App and LimaVM are in the 'all' category" {
    run -0 rdd ctl api-resources --categories=all --output=name
    assert_line apps.app.rancherdesktop.io
    assert_line limavms.lima.rancherdesktop.io
}

@test "verify 'get all' returns the LimaVM instance" {
    run -0 rdd ctl get all --namespace "${RDD_NAMESPACE}" --output=name
    assert_line "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "verify LimaVM is named rd" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o name
    assert_output "limavm.lima.rancherdesktop.io/${VM_NAME}"
}

@test "verify LimaVM has owned finalizer set by App controller" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/owned-by-App"
}

@test "verify LimaVM has cleanup finalizer set by LimaVM webhook" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.metadata.finalizers}'
    assert_output --partial "rdd.rancherdesktop.io/cleanup"
}

@test "verify LimaVM has owner reference pointing to App" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    local json="${output}"

    run -0 jq -r '.metadata.ownerReferences[0].kind' <<<"${json}"
    assert_output "App"

    run -0 jq -r '.metadata.ownerReferences[0].name' <<<"${json}"
    assert_output "${APP_NAME}"

    run -0 jq -r '.metadata.ownerReferences[0].controller' <<<"${json}"
    assert_output "true"
}

@test "verify LimaVM templateRef points to input ConfigMap" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.templateRef.name}'
    assert_output "${INPUT_CM_NAME}"
}

@test "verify LimaVM spec.running starts as false" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.running}'
    assert_output "false"
}

@test "wait for LimaVM webhook to copy template into rd-template ConfigMap" {
    rdd ctl wait --for=jsonpath='{.status.templateConfigMap}'="${VM_NAME}-template" \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "verify input ConfigMap is deleted after LimaVM copies it" {
    rdd ctl wait --for=delete configmap/"${INPUT_CM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "verify rd-template ConfigMap exists and contains template data" {
    run -0 rdd ctl get configmap "${VM_NAME}-template" \
        --namespace "${RDD_NAMESPACE}" -o jsonpath='{.data.template}'
    assert_output
}

@test "wait for LimaVM Created condition to be set" {
    # Download the opensuse distro image (~350MB) and decompress it. The
    # Windows CI runner is much slower than macOS/Linux here: xz runs
    # through Lima's stdin-wrapper pipe and is starved on every read,
    # stretching a ~34s operation to ~3 minutes. Budget generously until
    # that upstream slowness is addressed.
    rdd ctl wait --for=condition=Created \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=5m
}

@test "verify App status mirrors LimaVM Created condition" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    local json="${output}"

    run -0 jq -r '.status | has("conditions")' <<<"${json}"
    assert_output "true"

    run -0 jq -r '.status.conditions[] | select(.type == "Created") | .status' <<<"${json}"
    assert_output "True"
}

@test "verify App conditions preserve LimaVM LastTransitionTime" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Created") | .lastTransitionTime'
    local limavm_ts="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Created") | .lastTransitionTime'
    assert_output "${limavm_ts}"
}

@test "propagating running=true updates LimaVM spec.running" {
    rdd ctl patch app "${APP_NAME}" --type='merge' -p='{"spec":{"running":true}}'
    rdd ctl wait --for=jsonpath='{.spec.running}'=true \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=30s
}

@test "wait for LimaVM Running condition to become true after start" {
    rdd ctl wait --for=condition=Running \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=300s
}

@test "wait for App Running condition to become True" {
    rdd ctl wait --for=condition=Running app/"${APP_NAME}" --timeout=30s
}

@test "verify App mirrors LimaVM Running condition as True" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .status'
    assert_output "True"
}

@test "verify App Running condition preserves LimaVM LastTransitionTime" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .lastTransitionTime'
    local limavm_ts="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .lastTransitionTime'
    assert_output "${limavm_ts}"
}

@test "verify App Running condition observedGeneration reflects App generation" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.metadata.generation'
    local app_gen="${output}"

    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .observedGeneration'
    assert_output "${app_gen}"
}

@test "wait for dockerd to be ready with moby container engine" {
    # Per-try timeout guards against an ssh session that connects but then
    # blocks on an unresponsive dockerd socket inside the VM: `ssh` keeps
    # the channel alive via keepalives, so the default retry loop would
    # otherwise wait for the remote command forever.
    try --max 10 --delay 3 --per-try-timeout 30s \
        -- rdd limavm shell "${VM_NAME}" sudo docker info
}

@test "containerd is not running with moby container engine" {
    run rdd limavm shell "${VM_NAME}" sudo systemctl is-active containerd
    assert_line "inactive"
}

@test "switch container engine to containerd without stopping VM" {
    rdd set containerEngine.name=containerd
    # The reconciler detects the template change, stops the VM, and restarts it.
    rdd ctl wait --for=condition=Running=False \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=2m
    rdd ctl wait --for=condition=Running \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=7m
}

@test "wait for containerd to be ready with containerd engine" {
    try --max 10 --delay 3 -- rdd limavm shell "${VM_NAME}" sudo nerdctl --address /run/k3s/containerd/containerd.sock info
}

@test "dockerd is not running with containerd engine" {
    run rdd limavm shell "${VM_NAME}" sudo systemctl is-active docker
    assert_line "inactive"
}

@test "stop VM and restore moby engine after containerd test" {
    rdd set running=false containerEngine.name=moby
    rdd ctl wait --for=condition=Running=False \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "propagating running=false updates LimaVM spec.running" {
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" \
        -o jsonpath='{.spec.running}'
    assert_output "false"
}

@test "verify App mirrors LimaVM Running condition as False after stop" {
    run -0 rdd ctl get app "${APP_NAME}" -o json
    run -0 jq_output '.status.conditions[] | select(.type == "Running") | .status'
    assert_output "False"
}

@test "reject App resource with a name other than 'app'" {
    run -1 rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: not-app
spec:
  namespace: ${RDD_NAMESPACE}
  running: false
EOF
    assert_output --partial "App resource must be named 'app'"
}

@test "reject mutation of spec.namespace after creation" {
    run -1 rdd ctl patch app "${APP_NAME}" --type='merge' \
        -p='{"spec":{"namespace":"other-ns"}}'
    assert_output --partial "spec.namespace is immutable"
}

@test "reject direct deletion of LimaVM while owned by App" {
    run -1 rdd ctl delete limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}" --grace-period=0
    assert_output --partial "Forbidden"
    assert_output --partial "owned by App"
    assert_output --partial "delete the App resource instead"

    # Verify the LimaVM still exists and is not stuck in Terminating
    run -0 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}"
}

@test "delete App resource" {
    delete_app
}

@test "wait for App to be fully deleted" {
    # The App finalizer stays until the LimaVM controller finishes teardown.
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=90s
}

@test "verify LimaVM rd is deleted when App is deleted" {
    run -1 rdd ctl get limavm "${VM_NAME}" --namespace "${RDD_NAMESPACE}"
    assert_output --partial "not found"
}

@test "verify App can be recreated after deletion" {
    create_app false
    rdd ctl wait --for=create limavm/"${VM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=60s
    delete_app
}

@test "unsupported Kubernetes version is rejected by admission webhook" {
    delete_app
    # Exit code 3 is the cliexit.CodeRejected admission-rejection
    # signal added in this PR; see pkg/cli/exit/exit.go.
    run -3 rdd set running=false kubernetes.enabled=true kubernetes.version=1.31.99
    assert_output --partial "not supported"
}

@test "unsupported version does not create App resource" {
    run -1 rdd ctl get app "${APP_NAME}"
    assert_output --partial "not found"
}

@test "valid Kubernetes version allows LimaVM creation" {
    skip_on_windows "Kubernetes tests require further setup on Windows"
    delete_app
    rdd ctl apply -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: ${APP_NAME}
spec:
  namespace: ${RDD_NAMESPACE}
  running: false
  kubernetes:
    enabled: true
    version: "1.32.0"
EOF
    rdd ctl wait --for=create limavm/"${VM_NAME}" \
        --namespace "${RDD_NAMESPACE}" --timeout=60s
}

@test "start VM with Kubernetes enabled" {
    skip_on_windows "Kubernetes tests require further setup on Windows"
    # Use a generous timeout: rdd set now waits for KubernetesReady=True, which
    # requires VM boot + k3s startup. On a slow CI runner this can exceed the
    # default 300s, so give it up to 900s (the outer bats-with-timeout still
    # enforces the overall suite limit).
    rdd set --timeout=900s running=true
    rdd ctl wait --for=condition=Running \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=300s
}

@test "wait for k3s nodes to be Ready" {
    skip_on_windows "Kubernetes tests require further setup on Windows"
    # k3s.service goes active ~53s before the kubelet registers the node.
    # Poll the apiserver directly so we don't race past node registration.
    try --max 36 --delay 10 -- \
        rdd limavm shell "${VM_NAME}" sudo k3s kubectl wait \
        --for=condition=Ready node --all --timeout=5s
}

@test "kubernetes nodes are Ready" {
    skip_on_windows "Kubernetes tests require further setup on Windows"
    run -0 rdd limavm shell "${VM_NAME}" sudo k3s kubectl get nodes
    # Use surrounding spaces to avoid matching "NotReady".
    assert_output --partial " Ready "
}

@test "stop VM after Kubernetes test" {
    skip_on_windows "Kubernetes tests require further setup on Windows"
    rdd set running=false
    rdd ctl wait --for=condition=Running=False \
        limavm/"${VM_NAME}" --namespace "${RDD_NAMESPACE}" --timeout=60s
    delete_app
}

# --- Admission webhook: --dry-run=server validation ---

@test "webhook configuration is registered" {
    rdd ctl wait --for=create ValidatingWebhookConfiguration "${APP_VALIDATOR_CONFIG}" --timeout=60s

    run -0 rdd ctl get ValidatingWebhookConfiguration "${APP_VALIDATOR_CONFIG}" \
        -o jsonpath='{.webhooks[0].name}'
    assert_output "app-validator.app.rancherdesktop.io"
}

@test "dry-run accepts valid App settings" {
    run -0 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: containerd
  kubernetes:
    enabled: false
EOF
}

@test "dry-run accepts valid containerEngine moby" {
    run -0 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: moby
  kubernetes:
    enabled: false
EOF
}

@test "dry-run rejects invalid containerEngine name" {
    run -1 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: kvm
  kubernetes:
    enabled: false
EOF
    assert_output --partial "containerEngine.name"
}

@test "dry-run rejects kubernetes.enabled=true with empty version" {
    run -1 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: moby
  kubernetes:
    enabled: true
EOF
    assert_output --partial "kubernetes.version must not be empty"
}

@test "dry-run rejects unsupported kubernetes.version" {
    run -1 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: moby
  kubernetes:
    enabled: true
    version: "0.0.0"
EOF
    assert_output --partial "not supported"
}

@test "dry-run accepts supported kubernetes.version" {
    run -0 rdd ctl apply --dry-run=server -f - <<EOF
apiVersion: app.rancherdesktop.io/v1alpha1
kind: App
metadata:
  name: app
spec:
  running: false
  containerEngine:
    name: moby
  kubernetes:
    enabled: true
    version: "${K3S_VERSION}"
EOF
}

@test "webhook rejects invalid update via patch dry-run" {
    # The App may not exist here and might be deleted after kubernetes tests, so we create one.
    create_app false

    run -0 rdd ctl get app app -o jsonpath='{.spec.containerEngine.name}'
    assert_output "moby"

    run -1 rdd ctl patch app app --dry-run=server \
        --type=merge -p '{"spec":{"kubernetes":{"enabled":true,"version":"0.0.0"}}}'
    assert_output --partial "not supported"

    run -0 rdd ctl get app app -o jsonpath='{.spec.containerEngine.name}'
    assert_output "moby"
}
