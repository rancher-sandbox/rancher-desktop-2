# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Network commands against the rdd Docker engine: create, ls, inspect, rm,
# prune, connect/disconnect, and name-based DNS on a user-defined network.

local_setup_file() {
    start_docker_engine
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

@test "create and ls show a user-defined network" {
    ctrctl network create rdd-net
    run -0 ctrctl network ls --quiet --filter name=rdd-net
    assert_output
    ctrctl network rm rdd-net
}

@test "network inspect reports the bridge driver" {
    ctrctl network create rdd-net-inspect
    run -0 ctrctl network inspect --format '{{.Driver}}' rdd-net-inspect
    assert_output bridge
    ctrctl network rm rdd-net-inspect
}

@test "containers resolve each other by name on a user-defined network" {
    ctrctl network create rdd-net-dns
    ctrctl run --detach --name rdd-net-server --network rdd-net-dns "${IMAGE_BUSYBOX}" sleep 300
    run -0 ctrctl run --rm --network rdd-net-dns "${IMAGE_BUSYBOX}" ping -c 1 rdd-net-server
    assert_output --partial '1 packets received'
    ctrctl rm --force rdd-net-server
    ctrctl network rm rdd-net-dns
}

@test "connect and disconnect attach a container to a network" {
    skip_unless_docker "nerdctl has no network connect/disconnect"
    ctrctl network create rdd-net-conn
    ctrctl run --detach --name rdd-net-attach "${IMAGE_BUSYBOX}" sleep 300

    ctrctl network connect rdd-net-conn rdd-net-attach
    run -0 ctrctl inspect --format '{{json .NetworkSettings.Networks}}' rdd-net-attach
    assert_output --partial rdd-net-conn

    ctrctl network disconnect rdd-net-conn rdd-net-attach
    run -0 ctrctl inspect --format '{{json .NetworkSettings.Networks}}' rdd-net-attach
    refute_output --partial rdd-net-conn

    ctrctl rm --force rdd-net-attach
    ctrctl network rm rdd-net-conn
}

@test "network prune removes unused networks" {
    ctrctl network create rdd-net-prune
    ctrctl network prune --force
    run -0 ctrctl network ls --quiet --filter name=rdd-net-prune
    refute_output
}

@test "network inspect of an unknown network fails" {
    run ctrctl network inspect rdd-no-such-network
    assert_failure
}
