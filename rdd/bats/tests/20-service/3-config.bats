load '../../helpers/load'

@test 'get kubeconfig' {
    rdd svc delete
    run -0 rdd svc config
    assert_line "apiVersion: v1"
    assert_line "kind: Config"
}
