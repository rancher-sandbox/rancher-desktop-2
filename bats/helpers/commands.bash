EXE=""
PLATFORM=${OS}
if is_windows; then
    if using_windows_exe; then
        EXE=".exe"
        PLATFORM=win32
    else
        # WSL with Linux binary (not yet supported).
        PLATFORM=linux
    fi
fi

no_cr() {
    tr -d '\r'
}
ctrctl() {
    if using_docker; then
        docker "$@"
    else
        nerdctl "$@"
    fi
}
curl() {
    command "curl${EXE}" "$@"
}
rdd() {
    local arg
    local args=("$@")
    if is_windows; then
        args=()
        if is_msys; then
            # MSYS_NO_PATHCONV is set globally to protect URL-like paths
            # (e.g. /passthrough/demo/hello), so convert filesystem paths
            # to Windows format manually.
            for arg in "$@"; do
                if [[ "${arg}" =~ ^/([a-zA-Z]/|tmp(/|$)) ]]; then
                    # Drive-letter mounts (/c/...) and /tmp are always
                    # filesystem paths; convert without existence check.
                    args+=("$(cygpath -w "${arg}")")
                elif [[ "${arg}" == /* ]] && [[ -e "${arg}" ]]; then
                    args+=("$(cygpath -w "${arg}")")
                else
                    args+=("${arg}")
                fi
            done
        else
            # WSL: convert /mnt/... paths.
            for arg in "$@"; do
                if [[ "${arg}" != "${arg#/mnt/}" ]]; then
                    args+=("$(wslpath -w "${arg}")")
                else
                    args+=("${arg}")
                fi
            done
            # Adjust WSLENV to include everything starting with RDD_
            local env envs
            mapfile -t envs < <({
                tr : '\n' <<<"${WSLENV}"
                env | awk -F= '/^RDD_/ { print $1 }'
            } | sort -u || true)
            WSLENV=""
            for env in "${envs[@]}"; do
                WSLENV="${WSLENV}:${env}"
            done
            WSLENV=${WSLENV#:}
            export WSLENV
        fi
    fi
    "${PATH_REPO_ROOT}/bin/rdd${EXE}" "${args[@]}"
}
