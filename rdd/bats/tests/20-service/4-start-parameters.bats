load '../../helpers/load'

# Verify that --controllers selects which controllers run.
# The discovery ConfigMap tracks enabled controllers; CRDs persist
# across restarts and cannot indicate which controllers are active.

get_enabled_controllers() {
    run -0 rdd ctl get configmap rdd-controller-manager \
        --namespace=rdd-system --output=jsonpath='{.data.embedded}'
    run -0 jq_output '.enabledControllers[]'
}

@test 'test instance with no controllers parameter' {
    run -0 rdd svc delete
    run -0 rdd svc create --controllers=""
    run -0 rdd svc start
    # enabledControllers is null when no controllers are running.
    # Use "// empty" because ".enabledControllers[]" errors on null.
    run -0 rdd ctl get configmap rdd-controller-manager \
        --namespace=rdd-system --output=jsonpath='{.data.embedded}'
    run -0 jq_output '.enabledControllers // empty'
    refute_output
}

@test 'create instance with specific controllers parameter' {
    run -0 rdd svc delete
    run -0 rdd svc create --controllers="rdd"

    # Verify parameters were saved
    ARGS_JSON="${RDD_ARGS_FILE}"
    assert_file_exist "${ARGS_JSON}"
    assert_file_contains "${ARGS_JSON}" '"--controllers"'
    assert_file_contains "${ARGS_JSON}" '"rdd"'
}

@test 'start instance uses saved parameters by default' {
    run -0 rdd svc start
    get_enabled_controllers
    assert_line notary
    assert_line configmapreplicaset
    refute_line demo
}

@test 'start instance with parameter override' {
    run -0 rdd svc stop
    run -0 rdd svc start --controllers="*"
    get_enabled_controllers
    assert_line notary
    assert_line configmapreplicaset
    assert_line demo
}

@test 'start instance with controller parameter override' {
    run -0 rdd svc stop
    run -0 rdd svc start --controllers="app"
    get_enabled_controllers
    assert_line demo
    refute_line notary
    refute_line configmapreplicaset
}

@test 'start instance returns to default parameters after override' {
    run -0 rdd svc stop
    # Start with default parameters (should use saved rdd controllers)
    run -0 rdd svc start
    get_enabled_controllers
    assert_line notary
    assert_line configmapreplicaset
    refute_line demo
}
