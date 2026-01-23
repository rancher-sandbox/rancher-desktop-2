load '../../helpers/load'

# Namespace deletion tests - tests the built-in namespace controller
# that handles cleanup of resources when a namespace is deleted.

local_setup_file() {
    setup_rdd_control_plane "*"
}

@test 'namespace deletion with multiple resource types' {
    rdd ctl create namespace "multi-ns"

    # Create various core resources
    rdd ctl create configmap test-cm -n multi-ns --from-literal=key=value
    rdd ctl create secret generic test-secret -n multi-ns --from-literal=password=secret

    # Create custom resources from different API groups
    # ConfigMapReplicaSet (rdd.rancherdesktop.io)
    rdd ctl apply -f - <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: ConfigMapReplicaSet
metadata:
  name: test-cmrs
  namespace: multi-ns
spec:
  replicas: 2
  data:
    key: value
EOF

    # Notary (rdd.rancherdesktop.io)
    rdd ctl apply -f - <<EOF
apiVersion: rdd.rancherdesktop.io/v1alpha1
kind: Notary
metadata:
  name: test-notary
  namespace: multi-ns
spec:
  value: "test-value"
  configMapName: "notary-history"
EOF

    # LimaVM (lima.rancherdesktop.io)
    rdd ctl create configmap lima-template -n multi-ns --from-literal=template='images: [{"location":"https://example.invalid"}]'
    rdd ctl apply -f - <<EOF
apiVersion: lima.rancherdesktop.io/v1alpha1
kind: LimaVM
metadata:
  name: test-vm
  namespace: multi-ns
spec:
  running: false
  templateRef:
    name: lima-template
EOF

    # Verify all resources exist
    rdd ctl get configmap test-cm -n multi-ns
    rdd ctl get secret test-secret -n multi-ns
    rdd ctl get configmapreplicaset test-cmrs -n multi-ns
    rdd ctl get notary test-notary -n multi-ns
    rdd ctl get limavm test-vm -n multi-ns

    # Delete namespace
    rdd ctl delete namespace "multi-ns" --timeout=20s
}
