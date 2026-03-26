# Lima VM template selection for BATS tests.
#
# On Windows (WSL2), set RDD_WSL_DISTRO to choose between distros:
#   - "finch"    (default): Finch rootfs (Fedora-based, stable for testing)
#   - "opensuse":           rancher-desktop-opensuse (the target distro for RD2)
#
# Usage in test files:
#   VM_TEMPLATE=$(vm_template)
# Supports RDD_VM_TYPE on Unix (expands to `vmType: <value>` when set).

: "${RDD_WSL_DISTRO:=finch}"

vm_template() {
    if is_windows; then
        _wsl2_template
    else
        _unix_template
    fi
}

_wsl2_template() {
    case "${RDD_WSL_DISTRO}" in
    finch)
        cat <<'YAML'
vmType: wsl2
images:
- location: https://deps.runfinch.com/common/x86-64/finch-rootfs-production-amd64-1771357941.tar.gz
  arch: x86_64
  digest: sha256:423d1a0f1cabeaea6801995c90ed896dccc091180068626430f19fd87853fdf3
mountType: wsl2
containerd:
  system: false
  user: false
YAML
        ;;
    opensuse)
        cat <<'YAML'
vmType: wsl2
images:
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.1.2/distro.v0.1.2.amd64.tar.xz
  arch: x86_64
  digest: sha256:dab70ce152f163ad5c7ce45accb314b91a68f43efd9a153415eaab3e22b8cdf8
mountType: wsl2
containerd:
  system: false
  user: false
YAML
        ;;
    *)
        echo "Unknown RDD_WSL_DISTRO: ${RDD_WSL_DISTRO}" >&2
        return 1
        ;;
    esac
}

_unix_template() {
    cat <<YAML
images:
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.1.1/distro.v0.1.1.amd64.qcow2
  arch: x86_64
  digest: sha256:6a0a2729781f7a412f2d4fd7cb3270104eb16d9965811d0a39cb9766afdf3fd3
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.1.1/distro.v0.1.1.arm64.qcow2
  arch: aarch64
  digest: sha256:8e8f9dfa8292dd4e3821f44542305b01c78ec8cb007065d1bba233899ce438e8
${RDD_VM_TYPE:+vmType: ${RDD_VM_TYPE}}
containerd:
  system: false
  user: false
YAML
}
