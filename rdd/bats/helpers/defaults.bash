########################################################################
: "${RDD_INSTANCE=bats}"
export RDD_INSTANCE

: "${RDD_TRACE:=false}"
: "${RDD_NAMESPACE:=rdd-bats}"
: "${RDD_KEEP_LOGS:=1}"
export RDD_KEEP_LOGS

# Container engine under test. moby selects the Docker engine (and the docker
# CLI); containerd selects nerdctl. ctrctl() in commands.bash dispatches on it.
: "${RDD_CONTAINER_ENGINE:=moby}"

using_containerd() {
    [[ "${RDD_CONTAINER_ENGINE}" == containerd ]]
}

using_docker() {
    ! using_containerd
}

using_windows_exe() {
    # MSYS2 always uses Windows executables; there is no Linux alternative.
    if is_msys; then
        return 0
    fi
    # WSL: currently always uses Windows executables.
    # TODO: Support testing with the Linux binary on WSL.
    true
}
