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
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.2.2/distro.v0.2.2.amd64.tar.xz
  arch: x86_64
  digest: sha256:80aa8acb4f2784b44c0e4dd90e2dacb587623e93f2e72abe355d034a46e4542e
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
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.2.2/distro.v0.2.2.amd64.raw.xz
  arch: x86_64
  digest: sha256:a1aaeb12beca92d4d0ca62c19322dc217035db766c694ca287d243d6878f255c
- location: https://github.com/rancher-sandbox/rancher-desktop-opensuse/releases/download/v0.2.2/distro.v0.2.2.arm64.raw.xz
  arch: aarch64
  digest: sha256:dbd58f962e42d0b946929c9ab7f5126363d6b0d918e79cd6f2c24620d087e64a
${RDD_VM_TYPE:+vmType: ${RDD_VM_TYPE}}
containerd:
  system: false
  user: false
YAML
}
