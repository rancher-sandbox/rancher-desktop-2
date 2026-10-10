# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

local_setup_file() {
    setup_rdd_control_plane "container"
}

@test "ContainerCreateRequest accepts only the created and running states" {
    run -1 rdd ctl create --dry-run=server --filename=- <<EOF
apiVersion: containers.rancherdesktop.io/v1alpha1
kind: ContainerCreateRequest
metadata:
  name: paused-request
  namespace: default
spec:
  image: busybox
  state: paused
EOF
    assert_output --partial 'spec.state: Unsupported value: "paused": supported values: "created", "running"'
}
