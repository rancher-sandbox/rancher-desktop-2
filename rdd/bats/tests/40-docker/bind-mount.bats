# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Bind mounts of Windows host directories. The Docker API proxy rewrites
# their drive paths to the guest's /mnt/<drive> paths.

local_setup_file() {
    skip_on_unix "Only Windows host paths need translating."
    start_docker_engine
    ctrctl pull --quiet "${IMAGE_BUSYBOX}"
}

local_setup() {
    create_file "${BATS_TEST_TMPDIR}/shared/file" <<<marker
}

@test "--volume mounts a directory given as a backslash drive path" {
    run -0 winpath "${BATS_TEST_TMPDIR}/shared"
    volume="${output}:/data:ro"
    run -0 ctrctl run --rm --volume "${volume}" "${IMAGE_BUSYBOX}" cat /data/file
    assert_output marker
}

@test "--mount type=bind mounts a directory given as a forward-slash drive path" {
    run -0 host_path "${BATS_TEST_TMPDIR}/shared"
    mount="type=bind,source=${output},target=/data"
    run -0 ctrctl run --rm --mount "${mount}" "${IMAGE_BUSYBOX}" cat /data/file
    assert_output marker
}
