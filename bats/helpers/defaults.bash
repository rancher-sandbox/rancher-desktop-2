########################################################################
: "${RDD_INSTANCE=bats}"
export RDD_INSTANCE

: "${RDD_TRACE:=false}"
: "${RDD_NAMESPACE:=default}"
: "${RDD_KEEP_LOGS:=1}"
export RDD_KEEP_LOGS

using_windows_exe() {
    # MSYS2 always uses Windows executables; there is no Linux alternative.
    if is_msys; then
        return 0
    fi
    # WSL: currently always uses Windows executables.
    # TODO: Support testing with the Linux binary on WSL.
    true
}
