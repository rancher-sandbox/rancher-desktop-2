# SPDX-License-Identifier: Apache-2.0
# SPDX-FileCopyrightText: SUSE LLC
# SPDX-FileCopyrightText: The Rancher Desktop Authors

load '../../helpers/load'

# Tests for `rdd start` and `rdd stop`. Both are thin wrappers around
# `rdd set running=true|false`, so this suite focuses on what is unique
# to them: rdd stop short-circuits when the App is absent rather than
# creating it. Like set.bats, this runs with only the app controller
# (no lima, no engine), and uses --wait=false so the patches return
# without waiting on a Settled condition that never fires.

APP_NAME="app"

local_setup_file() {
    setup_rdd_control_plane "app"
    rdd ctl delete app "${APP_NAME}" --ignore-not-found
    rdd ctl wait --for=delete app/"${APP_NAME}" --timeout=30s 2>/dev/null || true
}

# --- Help (no App needed) ---

@test "rdd start --help shows usage" {
    run -0 rdd start --help
    assert_output --partial "Start"
    assert_output --partial "--wait"
    assert_output --partial "--timeout"
}

@test "rdd stop --help shows usage" {
    run -0 rdd stop --help
    assert_output --partial "Stop"
    assert_output --partial "--wait"
    assert_output --partial "--timeout"
}

# --- Short-circuit: rdd stop on missing App ---

@test "rdd stop succeeds when App is absent without creating it" {
    run -1 rdd ctl get app "${APP_NAME}"
    assert_output --partial "not found"

    run -0 rdd stop
    assert_output --partial "does not exist"

    # rdd stop must not provision the App as a side effect.
    run -1 rdd ctl get app "${APP_NAME}"
    assert_output --partial "not found"
}

# --- rdd start creates the App with running=true ---

@test "rdd start creates App with running=true" {
    rdd start --wait=false

    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.metadata.name}'
    assert_output "${APP_NAME}"

    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.spec.running}'
    assert_output "true"
}

# --- rdd stop patches existing App to running=false ---

@test "rdd stop patches existing App to running=false" {
    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.spec.running}'
    assert_output "true"

    rdd stop --wait=false

    run -0 rdd ctl get app "${APP_NAME}" -o jsonpath='{.spec.running}'
    assert_output "false"
}
