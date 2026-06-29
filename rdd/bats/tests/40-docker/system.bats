# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# System commands against the rdd Docker engine: version, info, events, df,
# prune.

local_setup_file() {
    start_docker_engine
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

@test "version reports the server version" {
    run -0 ctrctl version --format '{{.Server.Version}}'
    assert_output
}

@test "info reports the OS type" {
    run -0 ctrctl info --format '{{.OSType}}'
    assert_output linux
}

@test "events reports container activity in a time window" {
    since=$(date +%s)
    ctrctl run --rm "${IMAGE_BUSYBOX}" true
    until=$(($(date +%s) + 1))
    run -0 ctrctl events --since "${since}" --until "${until}"
    assert_output --partial create
}

@test "system df reports disk usage" {
    skip_unless_docker "nerdctl has no system df"
    run -0 ctrctl system df
    assert_output --partial Images
}

@test "system prune runs against the engine" {
    ctrctl system prune --force
}
