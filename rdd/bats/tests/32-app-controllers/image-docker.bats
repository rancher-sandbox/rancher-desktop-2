# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# ImagePullRequest controller tests against the real moby engine.

local_setup_file() {
    start_docker_engine

    RDD_NAMESPACE=$(rdd ctl get app app -o jsonpath='{.spec.namespace}')
    export RDD_NAMESPACE

    # Ensure moby namespace exists.
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" ContainerNamespace/moby --timeout=60s
}

@test "ImagePullRequest succeeds and sets Failed reason to Succeeded" {
    request_name="image-pull-success-${BATS_TEST_NUMBER}"
    rdd ctl apply -f - <<EOF
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImagePullRequest
metadata:
  name: ${request_name}
  namespace: ${RDD_NAMESPACE}
spec:
  namespace: moby
  repoTag: registry.suse.com/bci/bci-nano:latest
EOF

    wait_for_resource_condition ImagePullRequest "${request_name}" Settled status True
    wait_for_resource_condition ImagePullRequest "${request_name}" Failed status False
    wait_for_resource_condition ImagePullRequest "${request_name}" Failed reason Succeeded
    rdd ctl wait --for=create --namespace="${RDD_NAMESPACE}" image \
        --field-selector 'status.repoTag=registry.suse.com/bci/bci-nano:latest' --timeout=60s
}

@test "ImagePullRequest sets Failed reason to PullFailed when pull fails" {
    request_name="image-pull-fail-${BATS_TEST_NUMBER}"
    repo_tag="registry.example.invalid/does-not-exist:missing-tag"
    rdd ctl apply -f - <<EOF
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ImagePullRequest
metadata:
  name: ${request_name}
  namespace: ${RDD_NAMESPACE}
spec:
  namespace: moby
  repoTag: ${repo_tag}
EOF

    wait_for_resource_condition ImagePullRequest "${request_name}" Settled status True
    wait_for_resource_condition ImagePullRequest "${request_name}" Failed status True
    wait_for_resource_condition ImagePullRequest "${request_name}" Failed reason PullFailed

    run -0 rdd ctl get --output=name --namespace="${RDD_NAMESPACE}" image \
        --field-selector "status.repoTag=${repo_tag}"
    refute_output
}
