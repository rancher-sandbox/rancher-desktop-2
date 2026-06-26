# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Image commands against the rdd Docker engine: pull, images, tag, inspect,
# history, save/load, rmi, prune.

local_setup_file() {
    start_docker_engine
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

@test "images lists a pulled image" {
    run -0 ctrctl images --quiet "${IMAGE_BUSYBOX}"
    assert_output
}

@test "tag adds a second reference to an image" {
    ctrctl tag "${IMAGE_BUSYBOX}" rdd-tag:v1
    run -0 ctrctl images --quiet rdd-tag:v1
    assert_output
    ctrctl rmi rdd-tag:v1
}

@test "image inspect reports image fields" {
    run -0 ctrctl image inspect --format '{{.Os}}' "${IMAGE_BUSYBOX}"
    assert_output linux
}

@test "history lists an image's layers" {
    run -0 ctrctl history --quiet "${IMAGE_BUSYBOX}"
    assert_output
}

@test "save and load round-trip an image" {
    run -0 host_path "${BATS_TEST_TMPDIR}/image.tar"
    image_tar=${output}

    ctrctl tag "${IMAGE_BUSYBOX}" rdd-save:v1
    ctrctl save --output "${image_tar}" rdd-save:v1
    ctrctl rmi rdd-save:v1
    run -0 ctrctl images --quiet rdd-save:v1
    refute_output

    ctrctl load --input "${image_tar}"
    run -0 ctrctl images --quiet rdd-save:v1
    assert_output

    ctrctl rmi rdd-save:v1
}

@test "rmi removes an image tag" {
    ctrctl tag "${IMAGE_BUSYBOX}" rdd-rmi:v1
    ctrctl rmi rdd-rmi:v1
    run -0 ctrctl images --quiet rdd-rmi:v1
    refute_output
}

@test "image prune runs against the engine" {
    # With no dangling images this removes nothing, but the command must work.
    ctrctl image prune --force
}

@test "rmi of an unknown image fails" {
    run ctrctl rmi rdd-no-such-image:nope
    assert_failure
}
