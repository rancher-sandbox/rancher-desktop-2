# Implementation

## Milestones

* Working control plane, can access it with `kubectl`
* Control plane can background itself with `rdd service serve`
* Can use `rdd kubectl` as `kubectl` replacement
* Has `rdd ctl` command
* Can create a Lima VM with `rdd ctl apply`
* Can start the app with `rdd start`, which will launch the VM
* App implements the functionality needed for TwoCows BATS run
* BATS supports RDD
* Minimal GUI created by forking "Rancher Desktop 1.x"

## Details

### Create Control Plane application

* Start with example from Kubernetes repo
* Replace `etcd` with `kine` (check `k3s` repo how they do it)
* Make sure it works on Linux, macOS, and Windows
* Add/adjust Cobra settings to make it the `rdd service serve` command
* Implement backgrounding via fork & exec (Windows will come later)

### Integrate kubectl

* Start with latest Kubernetes version
* Check how `k3s` implements Kubernetes version upgrades while maintaining patches
* Implement `rdd kubectl`
* Implement `rdd service kubeconfig` as well as `start` and `stop`
* Implement `rdd ctl`

### Implement `LimaVM`

* Use ConfigMap for template
* Use TwoCows code to produce minimal working example
* Don't bother with `Resource` objects etc. Everything goes to fixed location in the filesystem

### Implement minimal `App`

* just one setting

### Implement `rdd start` etc

### Update BATS to optionally support RDD

### Fork GUI and strip down to minimal functionality
