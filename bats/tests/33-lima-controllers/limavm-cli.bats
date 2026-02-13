load '../../helpers/load'

# LimaVM CLI tests - tests the rdd limavm subcommands for managing LimaVM resources.

LIMA_TEST_NS="lima-test-ns"

# non-functional template, but passes Lima validation
TEMPLATE='images: [{"location":"https://foo"}]'

local_setup_file() {
    setup_rdd_control_plane "lima"
}

local_setup() {
    rdd ctl create namespace "${LIMA_TEST_NS}"
}

local_teardown() {
    rdd ctl delete namespace "${LIMA_TEST_NS}"
}

# assert_running verifies the "test-vm" running state, must be "true" or "false"
assert_running() {
    local state=$1
    run -0 rdd ctl get limavm "test-vm" --namespace "${LIMA_TEST_NS}" -o jsonpath='{.spec.running}'
    assert_output "${state}"
}

# assert_created verifies the $name VM has been created in $namespace from $template
# The `rdd limavm create` command must have been run with `run --separate-stderr`
# because we are checking $stderr for the log message.
assert_created() {
    local name=$1
    local namespace=$2
    local template=$3

    local msg
    printf -v msg 'LimaVM "%s" created in namespace "%s" with template ConfigMap "%s"' "${name}" "${namespace}" "${template}"
    assert_info "${msg}"
}

@test "lima create with missing ConfigMap template" {
    # Create VM with ConfigMap template
    run_e -1 rdd limavm create "test-missing-vm" "test-missing-template" --namespace "${LIMA_TEST_NS}"
    assert_stderr_line --partial 'denied the request'
    assert_stderr_line --partial 'ConfigMap \"test-missing-template\" not found'

    # Verify the ConfigMap was not created, or at least deleted after.
    run -1 rdd ctl get configmap "test-missing-template" --namespace "${LIMA_TEST_NS}" --output=name
    assert_output --partial NotFound

    # Verify the VM was not created
    run -0 rdd ctl get limavm --namespace "${LIMA_TEST_NS}" --output=name
    refute_output # There should be no output at all
}

@test "lima create/start/stop/delete with ConfigMap template" {
    # Create a template ConfigMap first
    rdd ctl create configmap "test-template" --namespace "${LIMA_TEST_NS}" --from-literal="template=${TEMPLATE}"

    # Create VM with ConfigMap template
    run_e -0 rdd limavm create "test-vm" "test-template" --namespace "${LIMA_TEST_NS}"
    assert_created "test-vm" "${LIMA_TEST_NS}" "test-template"
    assert_running "false"

    # Start the VM (cross-namespace lookup: no --namespace flag needed)
    run -0 rdd limavm start "test-vm"
    assert_output --partial 'started'
    assert_running "true"

    # Stop the VM
    run -0 rdd limavm stop "test-vm"
    assert_output --partial 'stopped'
    assert_running "false"

    # Delete the VM
    run -0 rdd limavm delete --wait "test-vm"
    assert_output --partial 'deleted'
    run -1 rdd ctl get limavm "test-vm" --namespace "${LIMA_TEST_NS}"
    assert_output --partial "not found"
}

@test "lima create with file template" {
    # Create a temporary template file
    local template_file="${BATS_TEST_TMPDIR}/test-template.yaml"
    echo "${TEMPLATE}" >"${template_file}"

    # Create VM with file template (dry run)
    run_e -0 rdd limavm create "test-vm-file" "${template_file}" --namespace "${LIMA_TEST_NS}" --dry-run
    # Generated ConfigMap has the same name as the LimaVM
    assert_created "test-vm-file" "${LIMA_TEST_NS}" "test-vm-file"

    # Verify the ConfigMap was not created, or at least deleted after.
    run -1 rdd ctl get configmap "test-vm-file" --namespace "${LIMA_TEST_NS}" --output=name
    assert_output --partial NotFound

    # Verify the VM was not created
    run -0 rdd ctl get limavm --namespace "${LIMA_TEST_NS}" --output=name
    refute_output # There should be no output at all

    # Create VM with file template
    run_e -0 rdd limavm create "test-vm-file" "${template_file}" --namespace "${LIMA_TEST_NS}"
    # Generated ConfigMap has the same name as the LimaVM
    assert_created "test-vm-file" "${LIMA_TEST_NS}" "test-vm-file"

    # Verify the ConfigMap was created
    run -0 rdd ctl get configmap "test-vm-file" --namespace "${LIMA_TEST_NS}" -o jsonpath='{.data.template}'
    assert_output "${TEMPLATE}"

    # Try creating another VM with same name - should fail because ConfigMap exists
    run -1 rdd limavm create "test-vm-file" "${template_file}" --namespace "${LIMA_TEST_NS}"
    assert_output --partial 'already exists'

    # Delete the LimaVM
    run -0 rdd limavm delete "test-vm-file"
    assert_output --partial 'deleted'

    # Wait for LimaVM to be fully deleted
    rdd ctl wait --for=delete "limavm/test-vm-file" --namespace "${LIMA_TEST_NS}" --timeout="30s"

    # Verify ConfigMap was auto-deleted by controller finalizer
    run -1 rdd ctl get configmap "test-vm-file" --namespace "${LIMA_TEST_NS}"
    assert_output --partial "not found"
}

@test "lima create cleans up ConfigMap on LimaVM creation failure" {
    # Create a LimaVM in the default namespace to trigger uniqueness constraint
    local template_file="${BATS_TEST_TMPDIR}/test-template.yaml"
    echo "${TEMPLATE}" >"${template_file}"
    rdd limavm create duplicate-vm "${template_file}" --namespace "default"

    # Try to create another LimaVM with the same name in a different namespace
    # This should fail due to cross-namespace uniqueness enforcement by admission webhook
    local template_file2="${BATS_TEST_TMPDIR}/test-template2.yaml"
    echo "${TEMPLATE}" >"${template_file2}"
    run -1 rdd limavm create duplicate-vm "${template_file2}" --namespace "${LIMA_TEST_NS}"
    assert_output --partial "admission webhook"
    assert_output --partial "Cleaning up created ConfigMap"

    # Verify the ConfigMap was cleaned up (should not exist in $LIMA_TEST_NS)
    run -1 rdd ctl get configmap "duplicate-vm" --namespace "${LIMA_TEST_NS}"
    assert_output --partial "not found"
}

@test "lima create fails with non-existent file" {
    run -1 rdd limavm create "test-vm" "${BATS_TEST_TMPDIR}/nonexistent/template.yaml" --namespace "${LIMA_TEST_NS}"
    assert_output --partial 'failed to read template file'
}

@test "lima commands fail gracefully when VM does not exist" {
    local not_found='LimaVM "nonexistent" not found in any namespace'

    run_e -1 rdd limavm start "nonexistent"
    assert_fatal "${not_found}"

    run_e -1 rdd limavm stop "nonexistent"
    assert_fatal "${not_found}"

    run_e -1 rdd limavm delete "nonexistent"
    assert_fatal "${not_found}"
}

@test "lima help text is displayed" {
    # lima is an alias of the limavm command
    run -0 rdd lima --help
    assert_output --partial "LimaVM virtual machines"
    assert_output --partial "Create a new LimaVM"
    assert_output --partial "Start a LimaVM"
    assert_output --partial "Stop a LimaVM"
    assert_output --partial "Delete a LimaVM"
    assert_output --partial "Show LimaVM logs"
}
