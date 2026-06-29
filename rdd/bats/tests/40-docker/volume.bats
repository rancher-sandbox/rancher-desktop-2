# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Volume commands against the rdd Docker engine: create, ls, inspect, rm,
# prune, and data persistence across containers.

local_setup_file() {
    start_docker_engine
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

@test "create and ls show a named volume" {
    ctrctl volume create rdd-vol
    run -0 ctrctl volume ls --quiet --filter name=rdd-vol
    assert_output rdd-vol
    ctrctl volume rm rdd-vol
}

@test "volume inspect reports the volume name" {
    ctrctl volume create rdd-vol-inspect
    run -0 ctrctl volume inspect --format '{{.Name}}' rdd-vol-inspect
    assert_output rdd-vol-inspect
    ctrctl volume rm rdd-vol-inspect
}

@test "a volume persists data across containers" {
    ctrctl volume create rdd-vol-data
    ctrctl run --rm --volume rdd-vol-data:/data "${IMAGE_BUSYBOX}" sh -c 'echo persisted >/data/file'
    run -0 ctrctl run --rm --volume rdd-vol-data:/data "${IMAGE_BUSYBOX}" cat /data/file
    assert_output persisted
    ctrctl volume rm rdd-vol-data
}

@test "rm removes a volume" {
    ctrctl volume create rdd-vol-rm
    ctrctl volume rm rdd-vol-rm
    run -0 ctrctl volume ls --quiet --filter name=rdd-vol-rm
    refute_output
}

@test "volume prune removes unused volumes" {
    # --all removes named volumes too; the default only prunes anonymous ones.
    ctrctl volume create rdd-vol-prune
    ctrctl volume prune --all --force
    run -0 ctrctl volume ls --quiet --filter name=rdd-vol-prune
    refute_output
}

@test "volume inspect of an unknown volume fails" {
    run ctrctl volume inspect rdd-no-such-volume
    assert_failure
}
