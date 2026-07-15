# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

APP_NAME="app"

delete_app() {
    rdd ctl delete app "${APP_NAME}" --ignore-not-found
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=30s 2>/dev/null || true
}

get_app_field() {
    rdd ctl get app "${APP_NAME}" -o jsonpath="{$1}"
}

local_setup_file() {
    setup_rdd_control_plane "app,rdd"
    delete_app
    rdd set --wait=false running=false
}

@test "HostInfo singleton reports its host limits at startup" {
    # The singleton is created with an empty Status and a follow-up reconcile
    # fills it, so waiting on the create alone would race that write.
    rdd ctl wait --for=jsonpath='{.status.cpus}' hostinfo/system --timeout=30s
}

@test "HostInfo has a positive cpu count" {
    run -0 rdd ctl get hostinfo system -o jsonpath='{.status.cpus}'
    assert_output_ge 1
}

@test "HostInfo reports memory as a quantity" {
    run -0 rdd ctl get hostinfo system -o jsonpath='{.status.memory}'
    # A quantity so the GUI can compare it against spec.virtualMachine.memory
    # without converting units; the byte value is asserted in the Go tests.
    assert_output --regexp '^[0-9]+(Ki|Mi|Gi)?$'
}

@test "rdd set --help lists virtualMachine properties" {
    run -0 rdd set --help
    assert_output --partial "virtualMachine.cpus"
    assert_output --partial "virtualMachine.memory"
}

@test "rdd set virtualMachine.cpus stores the value" {
    rdd set --wait=false virtualMachine.cpus=2

    run -0 get_app_field '.spec.virtualMachine.cpus'
    assert_output "2"
}

@test "rdd set virtualMachine.memory stores the value" {
    rdd set --wait=false virtualMachine.memory=4Gi

    run -0 get_app_field '.spec.virtualMachine.memory'
    assert_output "4Gi"
}

@test "rdd set preserves other fields when setting virtualMachine properties" {
    rdd set --wait=false virtualMachine.cpus=1

    run -0 get_app_field '.spec.virtualMachine.cpus'
    assert_output "1"

    run -0 get_app_field '.spec.running'
    assert_output "false"
}

@test "webhook rejects memory below the 2 GiB minimum" {
    run -1 rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p='{"spec":{"virtualMachine":{"memory":"1Gi"}}}'
    assert_output --partial "denied the request"
    assert_output --partial "less than the minimum"
}

@test "webhook rejects cpus exceeding host count" {
    run -0 rdd ctl get hostinfo system -o jsonpath='{.status.cpus}'
    excessive=$((output + 1))

    run -1 rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p="{\"spec\":{\"virtualMachine\":{\"cpus\":${excessive}}}}"
    assert_output --partial "denied the request"
    assert_output --partial "exceeds the host CPU count"
}

@test "webhook rejects memory exceeding host total" {
    run -1 rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p='{"spec":{"virtualMachine":{"memory":"999999Gi"}}}'
    assert_output --partial "denied the request"
    assert_output --partial "exceeds the host memory"
}

@test "rdd set virtualMachine.cpus=0 is defaulted by the admission controller" {
    rdd set --wait=false virtualMachine.cpus=0

    run -0 get_app_field '.spec.virtualMachine.cpus'
    # The mutating webhook replaces the unset 0 with a concrete count.
    assert_output_ge 1
}

@test "webhook rejects a negative cpu count" {
    run -1 rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p='{"spec":{"virtualMachine":{"cpus":-1}}}'
    assert_output --partial "denied the request"
    assert_output --partial "must not be negative"
}

@test "rdd set virtualMachine.memory= is defaulted by the admission controller" {
    rdd set --wait=false "virtualMachine.memory="

    run -0 get_app_field '.spec.virtualMachine.memory'
    # The mutating webhook replaces the cleared memory with a concrete default
    # (RD1 settings), so it must not fall back to Lima's own default of empty.
    assert_output
}

@test "webhook accepts valid cpus and memory" {
    rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p='{"spec":{"virtualMachine":{"cpus":1,"memory":"2Gi"}}}'
}

@test "webhook accepts cpus=0 and defaults it" {
    rdd ctl patch app "${APP_NAME}" \
        --type='merge' --dry-run=server \
        -p='{"spec":{"virtualMachine":{"cpus":0}}}'
}

# The tests above patch an App that local_setup_file created, which exercises
# only the update path. Admission dispatches create and update through separate
# methods, so the remaining tests delete the App first and must run last.

@test "rdd set stores virtualMachine properties when it creates the App" {
    delete_app

    rdd set --wait=false virtualMachine.cpus=1

    run -0 get_app_field '.spec.virtualMachine.cpus'
    assert_output "1"
}

@test "rdd set clears memory while creating the App and the defaulter refills it" {
    delete_app

    # The cleared property is a null in the create body, which the CRD schema
    # would reject for a quantity field; the create must omit it instead.
    rdd set --wait=false "virtualMachine.memory="

    run -0 get_app_field '.spec.virtualMachine.memory'
    assert_output
}

@test "webhook rejects cpus exceeding the host count on create" {
    run -0 rdd ctl get hostinfo system -o jsonpath='{.status.cpus}'
    excessive=$((output + 1))

    delete_app

    run rdd set --wait=false "virtualMachine.cpus=${excessive}"
    assert_failure
    assert_output --partial "exceeds the host CPU count"
}
